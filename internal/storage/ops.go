package storage

/*
#include <stdint.h>
extern int sqlgo_encode_cell(uint8_t *buf, uint32_t left_ptr, const uint8_t *key, uint16_t key_len, const uint8_t *val, uint16_t val_len);
extern void sqlgo_decode_cell_header(const uint8_t *data, uint16_t offset, uint32_t *left_ptr, uint16_t *key_len, uint16_t *val_len);
extern void sqlgo_clear_page(uint8_t *data, int size);
extern int sqlgo_key_cmp(const uint8_t *a, int alen, const uint8_t *b, int blen);
extern void sqlgo_encode_uint64_be(uint8_t *buf, uint64_t v);
extern uint64_t sqlgo_decode_uint64_be(const uint8_t *buf);
extern void sqlgo_copy_bytes(uint8_t *dst, const uint8_t *src, int n);
extern void sqlgo_zero_bytes(uint8_t *dst, int n);
extern int sqlgo_page_binary_search(const uint8_t *page_data, uint16_t cell_count, uint16_t header_size, const uint8_t *key, int key_len, int *found);
extern uint16_t sqlgo_page_free_space(const uint8_t *page_data, uint16_t page_size, uint16_t header_size, uint16_t cell_count);
extern void sqlgo_encode_uint32_be(uint8_t *buf, uint32_t v);
extern uint32_t sqlgo_decode_uint32_be(const uint8_t *buf);
extern void sqlgo_shift_cell_pointers(uint8_t *page_data, uint16_t header_size, uint16_t from_idx, uint16_t to_idx, int direction);
extern int sqlgo_encode_varint(uint8_t *buf, uint64_t v);
extern int sqlgo_decode_varint(const uint8_t *buf, uint64_t *out);
*/
import "C"
import "unsafe"

func cEncodeCell(buf []byte, leftPtr uint32, key, val []byte) int {
	var keyPtr, valPtr *C.uint8_t
	if len(key) > 0 {
		keyPtr = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	if len(val) > 0 {
		valPtr = (*C.uint8_t)(unsafe.Pointer(&val[0]))
	}
	return int(C.sqlgo_encode_cell(
		(*C.uint8_t)(unsafe.Pointer(&buf[0])),
		C.uint32_t(leftPtr),
		keyPtr, C.uint16_t(len(key)),
		valPtr, C.uint16_t(len(val)),
	))
}

func cDecodeCellHeader(data []byte, offset uint16) (uint32, uint16, uint16) {
	var lp C.uint32_t
	var kl, vl C.uint16_t
	C.sqlgo_decode_cell_header(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.uint16_t(offset),
		&lp, &kl, &vl,
	)
	return uint32(lp), uint16(kl), uint16(vl)
}

func cClearPage(data []byte) {
	C.sqlgo_clear_page((*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)))
}

func cKeyCmp(a, b []byte) int {
	var ap, bp *C.uint8_t
	if len(a) > 0 {
		ap = (*C.uint8_t)(unsafe.Pointer(&a[0]))
	}
	if len(b) > 0 {
		bp = (*C.uint8_t)(unsafe.Pointer(&b[0]))
	}
	return int(C.sqlgo_key_cmp(ap, C.int(len(a)), bp, C.int(len(b))))
}

func cEncodeUint64BE(buf []byte, v uint64) {
	C.sqlgo_encode_uint64_be((*C.uint8_t)(unsafe.Pointer(&buf[0])), C.uint64_t(v))
}

func cDecodeUint64BE(buf []byte) uint64 {
	return uint64(C.sqlgo_decode_uint64_be((*C.uint8_t)(unsafe.Pointer(&buf[0]))))
}

func cCopyBytes(dst, src []byte) {
	if len(src) == 0 || len(dst) == 0 {
		return
	}
	C.sqlgo_copy_bytes(
		(*C.uint8_t)(unsafe.Pointer(&dst[0])),
		(*C.uint8_t)(unsafe.Pointer(&src[0])),
		C.int(len(src)),
	)
}

func cZeroBytes(dst []byte) {
	if len(dst) == 0 {
		return
	}
	C.sqlgo_zero_bytes((*C.uint8_t)(unsafe.Pointer(&dst[0])), C.int(len(dst)))
}

func cPageBinarySearch(pageData []byte, cellCount, headerSize uint16, key []byte) (int, bool) {
	var found C.int
	var keyPtr *C.uint8_t
	if len(key) > 0 {
		keyPtr = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	idx := int(C.sqlgo_page_binary_search(
		(*C.uint8_t)(unsafe.Pointer(&pageData[0])),
		C.uint16_t(cellCount),
		C.uint16_t(headerSize),
		keyPtr, C.int(len(key)),
		&found,
	))
	return idx, found != 0
}

func cPageFreeSpace(pageData []byte, pageSize, headerSize, cellCount uint16) uint16 {
	return uint16(C.sqlgo_page_free_space(
		(*C.uint8_t)(unsafe.Pointer(&pageData[0])),
		C.uint16_t(pageSize),
		C.uint16_t(headerSize),
		C.uint16_t(cellCount),
	))
}

func cShiftCellPointers(pageData []byte, headerSize, fromIdx, toIdx uint16, direction int) {
	C.sqlgo_shift_cell_pointers(
		(*C.uint8_t)(unsafe.Pointer(&pageData[0])),
		C.uint16_t(headerSize),
		C.uint16_t(fromIdx),
		C.uint16_t(toIdx),
		C.int(direction),
	)
}
