package storage

import (
	"container/list"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

const (
	PageSize         = 4096
	HeaderSize       = 100
	MagicString      = "SQLGO"
	MagicSize        = 8
	FileVersion      = 3
	InvalidPage      = ^uint32(0)
	DefaultCacheSize = 2048
	walBufSize       = 128
)

var (
	ErrCorruptedDB  = errors.New("database file is corrupted")
	ErrInvalidPage  = errors.New("invalid page number")
	ErrPageFull     = errors.New("page is full")
	ErrDatabaseFull = errors.New("database is full")
)

type PageType byte

const (
	PageTypeLeaf     PageType = 0x0D
	PageTypeInternal PageType = 0x05
	PageTypeFree     PageType = 0x00
	PageTypeOverflow PageType = 0x02
)

type Page struct {
	ID    uint32
	Data  [PageSize]byte
	Dirty bool
	pins  int32
}

func (p *Page) Type() PageType     { return PageType(p.Data[0]) }
func (p *Page) SetType(t PageType) { p.Data[0] = byte(t); p.Dirty = true }

func (p *Page) CellCount() uint16 {
	return binary.BigEndian.Uint16(p.Data[1:3])
}

func (p *Page) SetCellCount(n uint16) {
	binary.BigEndian.PutUint16(p.Data[1:3], n)
	p.Dirty = true
}

func (p *Page) CellContentOffset() uint16 {
	v := binary.BigEndian.Uint16(p.Data[3:5])
	if v == 0 {
		return PageSize
	}
	return v
}

func (p *Page) SetCellContentOffset(offset uint16) {
	binary.BigEndian.PutUint16(p.Data[3:5], offset)
	p.Dirty = true
}

func (p *Page) RightmostPointer() uint32 {
	return binary.BigEndian.Uint32(p.Data[5:9])
}

func (p *Page) SetRightmostPointer(ptr uint32) {
	binary.BigEndian.PutUint32(p.Data[5:9], ptr)
	p.Dirty = true
}

func PageHeaderSize() uint16 { return 9 }

func (p *Page) FreeSpaceStart() uint16 {
	return PageHeaderSize() + p.CellCount()*2
}

func (p *Page) FreeSpace() uint16 {
	return cPageFreeSpace(p.Data[:], PageSize, PageHeaderSize(), p.CellCount())
}

func (p *Page) CellPointer(idx uint16) uint16 {
	offset := PageHeaderSize() + idx*2
	return binary.BigEndian.Uint16(p.Data[offset : offset+2])
}

func (p *Page) SetCellPointer(idx uint16, ptr uint16) {
	offset := PageHeaderSize() + idx*2
	binary.BigEndian.PutUint16(p.Data[offset:offset+2], ptr)
	p.Dirty = true
}

func (p *Page) InsertCellPointer(idx uint16, ptr uint16) {
	count := p.CellCount()
	if count > idx {
		cShiftCellPointers(p.Data[:], PageHeaderSize(), idx, count, 1)
	}
	p.SetCellPointer(idx, ptr)
}

func (p *Page) Pin()         { atomic.AddInt32(&p.pins, 1) }
func (p *Page) Unpin()       { atomic.AddInt32(&p.pins, -1) }
func (p *Page) Pinned() bool { return atomic.LoadInt32(&p.pins) > 0 }

type DBHeader struct {
	Magic      [MagicSize]byte
	Version    uint32
	PageSize   uint32
	PageCount  uint32
	FreeList   uint32
	RootPage   uint32
	SchemaPage uint32
}

func (h *DBHeader) Encode() []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:MagicSize], h.Magic[:])
	binary.BigEndian.PutUint32(buf[8:12], h.Version)
	binary.BigEndian.PutUint32(buf[12:16], h.PageSize)
	binary.BigEndian.PutUint32(buf[16:20], h.PageCount)
	binary.BigEndian.PutUint32(buf[20:24], h.FreeList)
	binary.BigEndian.PutUint32(buf[24:28], h.RootPage)
	binary.BigEndian.PutUint32(buf[28:32], h.SchemaPage)
	return buf
}

func DecodeHeader(buf []byte) (*DBHeader, error) {
	if len(buf) < HeaderSize {
		return nil, ErrCorruptedDB
	}
	h := &DBHeader{}
	copy(h.Magic[:], buf[0:MagicSize])
	h.Version = binary.BigEndian.Uint32(buf[8:12])
	h.PageSize = binary.BigEndian.Uint32(buf[12:16])
	h.PageCount = binary.BigEndian.Uint32(buf[16:20])
	h.FreeList = binary.BigEndian.Uint32(buf[20:24])
	h.RootPage = binary.BigEndian.Uint32(buf[24:28])
	h.SchemaPage = binary.BigEndian.Uint32(buf[28:32])
	return h, nil
}

type lruEntry struct {
	id   uint32
	page *Page
}

type lruCache struct {
	mu     sync.Mutex
	cap    int
	items  map[uint32]*list.Element
	order  *list.List
	hits   int64
	misses int64
}

func newLRUCache(cap int) *lruCache {
	return &lruCache{
		cap:   cap,
		items: make(map[uint32]*list.Element, cap),
		order: list.New(),
	}
}

func (c *lruCache) get(id uint32) (*Page, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		c.order.MoveToFront(el)
		atomic.AddInt64(&c.hits, 1)
		return el.Value.(*lruEntry).page, true
	}
	atomic.AddInt64(&c.misses, 1)
	return nil, false
}

func (c *lruCache) put(id uint32, pg *Page) *Page {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		c.order.MoveToFront(el)
		el.Value.(*lruEntry).page = pg
		return nil
	}
	var evicted *Page
	for c.order.Len() >= c.cap {
		el := c.order.Back()
		if el == nil {
			break
		}
		entry := el.Value.(*lruEntry)
		if entry.page.Pinned() {
			c.order.MoveToFront(el)
			break
		}
		delete(c.items, entry.id)
		c.order.Remove(el)
		evicted = entry.page
		if true {
			break
		}
	}
	el := c.order.PushFront(&lruEntry{id: id, page: pg})
	c.items[id] = el
	return evicted
}

func (c *lruCache) all() []*Page {
	c.mu.Lock()
	defer c.mu.Unlock()
	pages := make([]*Page, 0, c.order.Len())
	for el := c.order.Front(); el != nil; el = el.Next() {
		pages = append(pages, el.Value.(*lruEntry).page)
	}
	return pages
}

func (c *lruCache) remove(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		delete(c.items, id)
		c.order.Remove(el)
	}
}

func (c *lruCache) hitRate() float64 {
	h := atomic.LoadInt64(&c.hits)
	m := atomic.LoadInt64(&c.misses)
	if h+m == 0 {
		return 0
	}
	return float64(h) / float64(h+m)
}

type Pager struct {
	mu          sync.RWMutex
	file        *os.File
	header      *DBHeader
	cache       *lruCache
	walBuf      map[uint32]*Page
	walMu       sync.Mutex
	dirtyCount  int64
	freePages   []uint32
	freeMu      sync.Mutex
	totalReads  int64
	totalWrites int64
}

func NewPager(path string) (*Pager, error) {
	return NewPagerWithCacheSize(path, DefaultCacheSize)
}

func NewPagerWithCacheSize(path string, cacheSize int) (*Pager, error) {
	isNew := false
	if _, err := os.Stat(path); os.IsNotExist(err) {
		isNew = true
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	p := &Pager{
		file:   f,
		cache:  newLRUCache(cacheSize),
		walBuf: make(map[uint32]*Page, walBufSize),
	}
	if isNew {
		if err := p.initDB(); err != nil {
			f.Close()
			return nil, err
		}
	} else {
		if err := p.loadHeader(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return p, nil
}

func (p *Pager) initDB() error {
	h := &DBHeader{
		Version:    FileVersion,
		PageSize:   PageSize,
		PageCount:  2,
		FreeList:   InvalidPage,
		RootPage:   InvalidPage,
		SchemaPage: 1,
	}
	copy(h.Magic[:], []byte(MagicString))
	p.header = h
	headerBuf := h.Encode()
	paddedHeader := make([]byte, PageSize)
	copy(paddedHeader, headerBuf)
	if _, err := p.file.WriteAt(paddedHeader, 0); err != nil {
		return err
	}
	schemaPage := &Page{ID: 1}
	schemaPage.SetType(PageTypeLeaf)
	schemaPage.SetCellCount(0)
	schemaPage.SetCellContentOffset(PageSize)
	schemaPage.SetRightmostPointer(InvalidPage)
	return p.writePage(schemaPage)
}

func (p *Pager) loadHeader() error {
	buf := make([]byte, PageSize)
	if _, err := p.file.ReadAt(buf, 0); err != nil && err != io.EOF {
		return fmt.Errorf("read header: %w", err)
	}
	h, err := DecodeHeader(buf)
	if err != nil {
		return err
	}
	if string(h.Magic[:len(MagicString)]) != MagicString {
		return ErrCorruptedDB
	}
	p.header = h
	return nil
}

func (p *Pager) Header() *DBHeader { return p.header }

func (p *Pager) GetPage(id uint32) (*Page, error) {
	if pg, ok := p.cache.get(id); ok {
		return pg, nil
	}
	p.walMu.Lock()
	if pg, ok := p.walBuf[id]; ok {
		p.walMu.Unlock()
		return pg, nil
	}
	p.walMu.Unlock()

	pg := &Page{ID: id}
	offset := int64(id) * PageSize
	atomic.AddInt64(&p.totalReads, 1)
	n, err := p.file.ReadAt(pg.Data[:], offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read page %d: %w", id, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidPage, id)
	}
	evicted := p.cache.put(id, pg)
	if evicted != nil && evicted.Dirty {
		if err := p.writePage(evicted); err != nil {
			return nil, err
		}
	}
	return pg, nil
}

func (p *Pager) AllocatePage() (*Page, error) {
	p.freeMu.Lock()
	if len(p.freePages) > 0 {
		id := p.freePages[len(p.freePages)-1]
		p.freePages = p.freePages[:len(p.freePages)-1]
		p.freeMu.Unlock()
		pg := &Page{ID: id}
		evicted := p.cache.put(id, pg)
		if evicted != nil && evicted.Dirty {
			if err := p.writePage(evicted); err != nil {
				return nil, err
			}
		}
		return pg, nil
	}
	p.freeMu.Unlock()

	p.mu.Lock()
	id := p.header.PageCount
	p.header.PageCount++
	p.mu.Unlock()

	pg := &Page{ID: id}
	evicted := p.cache.put(id, pg)
	if evicted != nil && evicted.Dirty {
		if err := p.writePage(evicted); err != nil {
			return nil, err
		}
	}
	return pg, nil
}

func (p *Pager) FreePage(id uint32) {
	p.freeMu.Lock()
	p.freePages = append(p.freePages, id)
	p.freeMu.Unlock()
	p.cache.remove(id)
}

func (p *Pager) MarkDirty(pg *Page) {
	pg.Dirty = true
	if atomic.AddInt64(&p.dirtyCount, 1) >= walBufSize {
		go func() {
			_ = p.flushWAL()
			atomic.StoreInt64(&p.dirtyCount, 0)
		}()
	}
}

func (p *Pager) flushWAL() error {
	p.walMu.Lock()
	pages := make([]*Page, 0, len(p.walBuf))
	for _, pg := range p.walBuf {
		pages = append(pages, pg)
	}
	p.walBuf = make(map[uint32]*Page, walBufSize)
	p.walMu.Unlock()
	for _, pg := range pages {
		if pg.Dirty {
			if err := p.writePage(pg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pager) writePage(pg *Page) error {
	offset := int64(pg.ID) * PageSize
	atomic.AddInt64(&p.totalWrites, 1)
	if _, err := p.file.WriteAt(pg.Data[:], offset); err != nil {
		return fmt.Errorf("write page %d: %w", pg.ID, err)
	}
	pg.Dirty = false
	return nil
}

func (p *Pager) FlushAll() error {
	if err := p.flushWAL(); err != nil {
		return err
	}
	p.mu.Lock()
	headerBuf := p.header.Encode()
	paddedHeader := make([]byte, PageSize)
	copy(paddedHeader, headerBuf)
	if _, err := p.file.WriteAt(paddedHeader, 0); err != nil {
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()
	pages := p.cache.all()
	for _, pg := range pages {
		if pg.Dirty {
			if err := p.writePage(pg); err != nil {
				return err
			}
		}
	}
	return p.file.Sync()
}

func (p *Pager) Close() error {
	if err := p.FlushAll(); err != nil {
		return err
	}
	return p.file.Close()
}

func (p *Pager) PageCount() uint32 { return p.header.PageCount }

func (p *Pager) Stats() map[string]interface{} {
	return map[string]interface{}{
		"page_count":     p.header.PageCount,
		"cache_hit_rate": p.cache.hitRate(),
		"total_reads":    atomic.LoadInt64(&p.totalReads),
		"total_writes":   atomic.LoadInt64(&p.totalWrites),
		"free_pages":     len(p.freePages),
	}
}
