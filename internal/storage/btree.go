package storage

import (
	"fmt"
)

const (
	MaxCellSize     = 512
	MinCellsPerPage = 2
)

type Cell struct {
	Key     []byte
	Value   []byte
	LeftPtr uint32
}

func EncodeCell(c *Cell) []byte {
	keyLen := uint16(len(c.Key))
	valLen := uint16(len(c.Value))
	size := 8 + int(keyLen) + int(valLen)
	buf := make([]byte, size)
	cEncodeCell(buf, c.LeftPtr, c.Key, c.Value)
	return buf
}

func DecodeCell(data []byte, offset uint16) (*Cell, error) {
	if int(offset)+8 > len(data) {
		return nil, fmt.Errorf("cell offset out of bounds: %d", offset)
	}
	leftPtr, keyLen, valLen := cDecodeCellHeader(data, offset)
	end := int(offset) + 8 + int(keyLen) + int(valLen)
	if end > len(data) {
		return nil, fmt.Errorf("cell data out of bounds")
	}
	c := &Cell{
		LeftPtr: leftPtr,
		Key:     make([]byte, keyLen),
		Value:   make([]byte, valLen),
	}
	dataStart := int(offset) + 8
	cCopyBytes(c.Key, data[dataStart:dataStart+int(keyLen)])
	cCopyBytes(c.Value, data[dataStart+int(keyLen):dataStart+int(keyLen)+int(valLen)])
	return c, nil
}

func keyCmp(a, b []byte) int {
	return cKeyCmp(a, b)
}

type BTree struct {
	pager    *Pager
	rootPage uint32
}

func NewBTree(pager *Pager, root uint32) *BTree {
	return &BTree{pager: pager, rootPage: root}
}

func (bt *BTree) Root() uint32 { return bt.rootPage }

func (bt *BTree) Get(key []byte) ([]byte, bool, error) {
	return bt.search(bt.rootPage, key)
}

func (bt *BTree) search(pageID uint32, key []byte) ([]byte, bool, error) {
	for pageID != InvalidPage {
		pg, err := bt.pager.GetPage(pageID)
		if err != nil {
			return nil, false, err
		}
		count := pg.CellCount()
		pt := pg.Type()

		idx, found := cPageBinarySearch(pg.Data[:], count, PageHeaderSize(), key)

		if found {
			cell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(idx)))
			if err != nil {
				return nil, false, err
			}
			if pt == PageTypeLeaf {
				return cell.Value, true, nil
			}
			pageID = cell.LeftPtr
			continue
		}

		if pt == PageTypeInternal {
			if idx >= int(count) {
				pageID = pg.RightmostPointer()
			} else {
				cell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(idx)))
				if err != nil {
					return nil, false, err
				}
				pageID = cell.LeftPtr
			}
			continue
		}
		return nil, false, nil
	}
	return nil, false, nil
}

func (bt *BTree) Insert(key, value []byte) error {
	if bt.rootPage == InvalidPage {
		pg, err := bt.pager.AllocatePage()
		if err != nil {
			return err
		}
		pg.SetType(PageTypeLeaf)
		pg.SetCellCount(0)
		pg.SetCellContentOffset(PageSize)
		pg.SetRightmostPointer(InvalidPage)
		pg.Dirty = true
		bt.rootPage = pg.ID
		bt.pager.header.RootPage = pg.ID
	}
	overflow, midKey, midVal, newPage, err := bt.insertInto(bt.rootPage, key, value)
	if err != nil {
		return err
	}
	if overflow {
		newRoot, err := bt.pager.AllocatePage()
		if err != nil {
			return err
		}
		newRoot.SetType(PageTypeInternal)
		newRoot.SetCellCount(0)
		newRoot.SetCellContentOffset(PageSize)
		newRoot.SetRightmostPointer(newPage)
		newRoot.Dirty = true
		c := &Cell{Key: midKey, Value: midVal, LeftPtr: bt.rootPage}
		if err := insertCellIntoPage(newRoot, c); err != nil {
			return err
		}
		bt.rootPage = newRoot.ID
		bt.pager.header.RootPage = newRoot.ID
	}
	return nil
}

func (bt *BTree) insertInto(pageID uint32, key, value []byte) (bool, []byte, []byte, uint32, error) {
	pg, err := bt.pager.GetPage(pageID)
	if err != nil {
		return false, nil, nil, 0, err
	}
	if pg.Type() == PageTypeLeaf {
		return bt.insertLeaf(pg, key, value)
	}
	count := int(pg.CellCount())
	idx, _ := cPageBinarySearch(pg.Data[:], pg.CellCount(), PageHeaderSize(), key)

	var childPtr uint32
	if idx >= count {
		childPtr = pg.RightmostPointer()
	} else {
		cell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(idx)))
		if err != nil {
			return false, nil, nil, 0, err
		}
		childPtr = cell.LeftPtr
	}
	overflow, midKey, midVal, newPage, err := bt.insertInto(childPtr, key, value)
	if err != nil {
		return false, nil, nil, 0, err
	}
	if !overflow {
		return false, nil, nil, 0, nil
	}
	c := &Cell{Key: midKey, Value: midVal, LeftPtr: childPtr}
	if idx >= count {
		pg.SetRightmostPointer(newPage)
		c.LeftPtr = childPtr
	} else {
		oldCell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(idx)))
		if err != nil {
			return false, nil, nil, 0, err
		}
		oldCell.LeftPtr = newPage
		if err := rewriteCell(pg, uint16(idx), oldCell); err != nil {
			return false, nil, nil, 0, err
		}
		c.LeftPtr = childPtr
	}
	if err := insertCellIntoPage(pg, c); err != nil {
		if err == ErrPageFull {
			return bt.splitInternal(pg, c)
		}
		return false, nil, nil, 0, err
	}
	return false, nil, nil, 0, nil
}

func (bt *BTree) insertLeaf(pg *Page, key, value []byte) (bool, []byte, []byte, uint32, error) {
	count := int(pg.CellCount())
	idx, found := cPageBinarySearch(pg.Data[:], pg.CellCount(), PageHeaderSize(), key)
	if found {
		return false, nil, nil, 0, bt.updateLeafCell(pg, uint16(idx), key, value)
	}
	c := &Cell{Key: key, Value: value, LeftPtr: InvalidPage}
	if err := insertCellIntoPageAt(pg, c, uint16(idx)); err != nil {
		if err == ErrPageFull {
			return bt.splitLeaf(pg, key, value)
		}
		return false, nil, nil, 0, err
	}
	_ = count
	return false, nil, nil, 0, nil
}

func (bt *BTree) updateLeafCell(pg *Page, idx uint16, key, value []byte) error {
	return rewriteCell(pg, idx, &Cell{Key: key, Value: value, LeftPtr: InvalidPage})
}

func (bt *BTree) splitLeaf(pg *Page, newKey, newValue []byte) (bool, []byte, []byte, uint32, error) {
	cells, err := collectCells(pg)
	if err != nil {
		return false, nil, nil, 0, err
	}
	newCell := &Cell{Key: newKey, Value: newValue, LeftPtr: InvalidPage}
	cells = insertSorted(cells, newCell)
	mid := len(cells) / 2
	newPg, err := bt.pager.AllocatePage()
	if err != nil {
		return false, nil, nil, 0, err
	}
	newPg.SetType(PageTypeLeaf)
	newPg.SetCellCount(0)
	newPg.SetCellContentOffset(PageSize)
	newPg.SetRightmostPointer(InvalidPage)
	newPg.Dirty = true
	clearPage(pg)
	for _, c := range cells[:mid] {
		if err := insertCellIntoPage(pg, c); err != nil {
			return false, nil, nil, 0, err
		}
	}
	for _, c := range cells[mid:] {
		if err := insertCellIntoPage(newPg, c); err != nil {
			return false, nil, nil, 0, err
		}
	}
	return true, cells[mid].Key, cells[mid].Value, newPg.ID, nil
}

func (bt *BTree) splitInternal(pg *Page, newCell *Cell) (bool, []byte, []byte, uint32, error) {
	cells, err := collectCells(pg)
	if err != nil {
		return false, nil, nil, 0, err
	}
	cells = insertSorted(cells, newCell)
	mid := len(cells) / 2
	midCell := cells[mid]
	newPg, err := bt.pager.AllocatePage()
	if err != nil {
		return false, nil, nil, 0, err
	}
	newPg.SetType(PageTypeInternal)
	newPg.SetCellCount(0)
	newPg.SetCellContentOffset(PageSize)
	newPg.Dirty = true
	clearPage(pg)
	for _, c := range cells[:mid] {
		if err := insertCellIntoPage(pg, c); err != nil {
			return false, nil, nil, 0, err
		}
	}
	pg.SetRightmostPointer(midCell.LeftPtr)
	if len(cells) > mid+1 {
		right := cells[mid+1:]
		for i, c := range right {
			if i < len(right)-1 {
				if err := insertCellIntoPage(newPg, c); err != nil {
					return false, nil, nil, 0, err
				}
			}
		}
		newPg.SetRightmostPointer(cells[len(cells)-1].LeftPtr)
	}
	return true, midCell.Key, midCell.Value, newPg.ID, nil
}

func (bt *BTree) Delete(key []byte) (bool, error) {
	return bt.deleteFrom(bt.rootPage, key)
}

func (bt *BTree) deleteFrom(pageID uint32, key []byte) (bool, error) {
	if pageID == InvalidPage {
		return false, nil
	}
	pg, err := bt.pager.GetPage(pageID)
	if err != nil {
		return false, err
	}
	count := int(pg.CellCount())

	idx, found := cPageBinarySearch(pg.Data[:], pg.CellCount(), PageHeaderSize(), key)

	if found {
		cell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(idx)))
		if err != nil {
			return false, err
		}
		if pg.Type() == PageTypeLeaf {
			removeCellAt(pg, uint16(idx))
			pg.Dirty = true
			return true, nil
		}
		leafKey, leafVal, leafPage, err := bt.getLeftmost(cell.LeftPtr)
		if err != nil {
			return false, err
		}
		cell.Key = leafKey
		cell.Value = leafVal
		if err := rewriteCell(pg, uint16(idx), cell); err != nil {
			return false, err
		}
		return bt.deleteFrom(leafPage, leafKey)
	}

	if pg.Type() == PageTypeInternal {
		var childPtr uint32
		if idx >= count {
			childPtr = pg.RightmostPointer()
		} else {
			cell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(idx)))
			if err != nil {
				return false, err
			}
			childPtr = cell.LeftPtr
		}
		return bt.deleteFrom(childPtr, key)
	}
	return false, nil
}

func (bt *BTree) getLeftmost(pageID uint32) ([]byte, []byte, uint32, error) {
	pg, err := bt.pager.GetPage(pageID)
	if err != nil {
		return nil, nil, 0, err
	}
	if pg.Type() == PageTypeLeaf {
		if pg.CellCount() == 0 {
			return nil, nil, 0, fmt.Errorf("empty leaf page")
		}
		cell, err := DecodeCell(pg.Data[:], pg.CellPointer(0))
		if err != nil {
			return nil, nil, 0, err
		}
		return cell.Key, cell.Value, pageID, nil
	}
	cell, err := DecodeCell(pg.Data[:], pg.CellPointer(0))
	if err != nil {
		return nil, nil, 0, err
	}
	return bt.getLeftmost(cell.LeftPtr)
}

type Cursor struct {
	bt    *BTree
	stack []cursorFrame
	atEnd bool
}

type cursorFrame struct {
	pageID uint32
	idx    int
}

func (bt *BTree) NewCursor() (*Cursor, error) {
	c := &Cursor{bt: bt}
	if bt.rootPage == InvalidPage {
		c.atEnd = true
		return c, nil
	}
	if err := c.rewind(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Cursor) rewind() error {
	c.stack = nil
	c.atEnd = false
	pageID := c.bt.rootPage
	for {
		if pageID == InvalidPage {
			c.atEnd = true
			return nil
		}
		pg, err := c.bt.pager.GetPage(pageID)
		if err != nil {
			return err
		}
		if pg.Type() == PageTypeLeaf {
			if pg.CellCount() == 0 {
				c.atEnd = true
				return nil
			}
			c.stack = append(c.stack, cursorFrame{pageID: pageID, idx: 0})
			return nil
		}
		c.stack = append(c.stack, cursorFrame{pageID: pageID, idx: 0})
		cell, err := DecodeCell(pg.Data[:], pg.CellPointer(0))
		if err != nil {
			return err
		}
		pageID = cell.LeftPtr
	}
}

func (c *Cursor) AtEnd() bool { return c.atEnd }

func (c *Cursor) Current() (*Cell, error) {
	if c.atEnd || len(c.stack) == 0 {
		return nil, nil
	}
	frame := c.stack[len(c.stack)-1]
	pg, err := c.bt.pager.GetPage(frame.pageID)
	if err != nil {
		return nil, err
	}
	return DecodeCell(pg.Data[:], pg.CellPointer(uint16(frame.idx)))
}

func (c *Cursor) Advance() error {
	if c.atEnd {
		return nil
	}
	for len(c.stack) > 0 {
		frame := &c.stack[len(c.stack)-1]
		pg, err := c.bt.pager.GetPage(frame.pageID)
		if err != nil {
			return err
		}
		if pg.Type() == PageTypeLeaf {
			frame.idx++
			if frame.idx < int(pg.CellCount()) {
				return nil
			}
			c.stack = c.stack[:len(c.stack)-1]
		} else {
			frame.idx++
			count := int(pg.CellCount())
			var childPtr uint32
			if frame.idx < count {
				cell, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(frame.idx)))
				if err != nil {
					return err
				}
				childPtr = cell.LeftPtr
			} else if frame.idx == count {
				childPtr = pg.RightmostPointer()
			} else {
				c.stack = c.stack[:len(c.stack)-1]
				continue
			}
			pageID := childPtr
			for {
				if pageID == InvalidPage {
					break
				}
				childPg, err := c.bt.pager.GetPage(pageID)
				if err != nil {
					return err
				}
				if childPg.Type() == PageTypeLeaf {
					if childPg.CellCount() > 0 {
						c.stack = append(c.stack, cursorFrame{pageID: pageID, idx: 0})
					}
					return nil
				}
				c.stack = append(c.stack, cursorFrame{pageID: pageID, idx: 0})
				firstCell, err := DecodeCell(childPg.Data[:], childPg.CellPointer(0))
				if err != nil {
					return err
				}
				pageID = firstCell.LeftPtr
			}
			return nil
		}
	}
	c.atEnd = true
	return nil
}

func collectCells(pg *Page) ([]*Cell, error) {
	count := int(pg.CellCount())
	cells := make([]*Cell, 0, count)
	for i := 0; i < count; i++ {
		c, err := DecodeCell(pg.Data[:], pg.CellPointer(uint16(i)))
		if err != nil {
			return nil, err
		}
		cells = append(cells, c)
	}
	return cells, nil
}

func insertSorted(cells []*Cell, newCell *Cell) []*Cell {
	idx := len(cells)
	for i, c := range cells {
		if keyCmp(newCell.Key, c.Key) < 0 {
			idx = i
			break
		}
	}
	cells = append(cells, nil)
	copy(cells[idx+1:], cells[idx:])
	cells[idx] = newCell
	return cells
}

func clearPage(pg *Page) {
	t := pg.Type()
	rp := pg.RightmostPointer()
	cClearPage(pg.Data[:])
	pg.SetType(t)
	pg.SetCellCount(0)
	pg.SetCellContentOffset(PageSize)
	pg.SetRightmostPointer(rp)
	pg.Dirty = true
}

func insertCellIntoPage(pg *Page, c *Cell) error {
	count := pg.CellCount()
	encoded := EncodeCell(c)
	needed := uint16(len(encoded)) + 2
	if pg.FreeSpace() < needed {
		return ErrPageFull
	}
	newOffset := pg.CellContentOffset() - uint16(len(encoded))
	cCopyBytes(pg.Data[newOffset:], encoded)
	pg.SetCellContentOffset(newOffset)
	pg.SetCellPointer(count, newOffset)
	pg.SetCellCount(count + 1)
	pg.Dirty = true
	return nil
}

func insertCellIntoPageAt(pg *Page, c *Cell, idx uint16) error {
	count := pg.CellCount()
	encoded := EncodeCell(c)
	needed := uint16(len(encoded)) + 2
	if pg.FreeSpace() < needed {
		return ErrPageFull
	}
	newOffset := pg.CellContentOffset() - uint16(len(encoded))
	cCopyBytes(pg.Data[newOffset:], encoded)
	pg.SetCellContentOffset(newOffset)
	pg.InsertCellPointer(idx, newOffset)
	pg.SetCellCount(count + 1)
	pg.Dirty = true
	return nil
}

func rewriteCell(pg *Page, idx uint16, c *Cell) error {
	encoded := EncodeCell(c)
	offset := pg.CellPointer(idx)
	if int(offset)+len(encoded) > PageSize {
		return fmt.Errorf("cell rewrite out of bounds")
	}
	cCopyBytes(pg.Data[offset:], encoded)
	pg.Dirty = true
	return nil
}

func removeCellAt(pg *Page, idx uint16) {
	count := pg.CellCount()
	cShiftCellPointers(pg.Data[:], PageHeaderSize(), idx, count-1, -1)
	pg.SetCellCount(count - 1)
	pg.Dirty = true
}
