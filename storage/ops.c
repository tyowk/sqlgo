#include <stdint.h>
#include <string.h>
#include <stdlib.h>

int sqlgo_encode_cell(
		uint8_t *buf,
		uint32_t left_ptr,
		const uint8_t *key,
		uint16_t key_len,
		const uint8_t *val,
		uint16_t val_len
) {
		buf[0] = (uint8_t)(left_ptr >> 24);
		buf[1] = (uint8_t)(left_ptr >> 16);
		buf[2] = (uint8_t)(left_ptr >> 8);
		buf[3] = (uint8_t)(left_ptr);
		buf[4] = (uint8_t)(key_len >> 8);
		buf[5] = (uint8_t)(key_len);
		buf[6] = (uint8_t)(val_len >> 8);
		buf[7] = (uint8_t)(val_len);
		if (key_len > 0 && key != NULL) {
				memcpy(buf + 8, key, key_len);
		}
		if (val_len > 0 && val != NULL) {
				memcpy(buf + 8 + key_len, val, val_len);
		}
		return 8 + (int)key_len + (int)val_len;
}

void sqlgo_decode_cell_header(
		const uint8_t *data,
		uint16_t offset,
		uint32_t *left_ptr,
		uint16_t *key_len,
		uint16_t *val_len
) {
		const uint8_t *p = data + offset;
		*left_ptr = ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) | ((uint32_t)p[2] << 8) | (uint32_t)p[3];
		*key_len  = ((uint16_t)p[4] << 8) | (uint16_t)p[5];
		*val_len  = ((uint16_t)p[6] << 8) | (uint16_t)p[7];
}

void sqlgo_clear_page(uint8_t *data, int size) {
		memset(data + 1, 0, size - 1);
}

int sqlgo_key_cmp(
		const uint8_t *a, int alen,
		const uint8_t *b, int blen
) {
		int minlen = (alen < blen) ? alen : blen;
		int r = (alen == 0 || blen == 0) ? 0 : memcmp(a, b, minlen);
		if (r != 0) return r;
		if (alen < blen) return -1;
		if (alen > blen) return 1;
		return 0;
}

void sqlgo_encode_uint64_be(uint8_t *buf, uint64_t v) {
		buf[0] = (uint8_t)(v >> 56);
		buf[1] = (uint8_t)(v >> 48);
		buf[2] = (uint8_t)(v >> 40);
		buf[3] = (uint8_t)(v >> 32);
		buf[4] = (uint8_t)(v >> 24);
		buf[5] = (uint8_t)(v >> 16);
		buf[6] = (uint8_t)(v >> 8);
		buf[7] = (uint8_t)(v);
}

uint64_t sqlgo_decode_uint64_be(const uint8_t *buf) {
		return ((uint64_t)buf[0] << 56) | ((uint64_t)buf[1] << 48) |
		       ((uint64_t)buf[2] << 40) | ((uint64_t)buf[3] << 32) |
		       ((uint64_t)buf[4] << 24) | ((uint64_t)buf[5] << 16) |
		       ((uint64_t)buf[6] << 8)  | (uint64_t)buf[7];
}

void sqlgo_copy_bytes(uint8_t *dst, const uint8_t *src, int n) {
		if (n > 0 && dst != NULL && src != NULL) {
				memcpy(dst, src, n);
		}
}

void sqlgo_zero_bytes(uint8_t *dst, int n) {
		if (n > 0 && dst != NULL) {
				memset(dst, 0, n);
		}
}

int sqlgo_page_binary_search(
		const uint8_t *page_data,
		uint16_t cell_count,
		uint16_t header_size,
		const uint8_t *key,
		int key_len,
		int *found
) {
		int lo = 0, hi = (int)cell_count - 1, mid = 0;
		*found = 0;
		while (lo <= hi) {
				mid = (lo + hi) / 2;
				uint16_t ptr_off = header_size + (uint16_t)mid * 2;
				uint16_t cell_off = ((uint16_t)page_data[ptr_off] << 8) | page_data[ptr_off + 1];
				const uint8_t *p = page_data + cell_off;
				uint16_t klen = ((uint16_t)p[4] << 8) | p[5];
				int minlen = (key_len < (int)klen) ? key_len : (int)klen;
				int cmp = (minlen == 0) ? 0 : memcmp(key, p + 8, minlen);
				if (cmp == 0) {
						if (key_len < (int)klen) cmp = -1;
						else if (key_len > (int)klen) cmp = 1;
				}
				if (cmp == 0) { *found = 1; return mid; }
				else if (cmp < 0) hi = mid - 1;
				else lo = mid + 1;
		}
		return lo;
}

uint16_t sqlgo_page_free_space(
		const uint8_t *page_data,
		uint16_t page_size,
		uint16_t header_size,
		uint16_t cell_count
) {
		uint16_t content_off = ((uint16_t)page_data[3] << 8) | page_data[4];
		if (content_off == 0) content_off = page_size;
		uint16_t free_start = header_size + cell_count * 2;
		if (content_off <= free_start) return 0;
		return content_off - free_start;
}

void sqlgo_encode_uint32_be(uint8_t *buf, uint32_t v) {
		buf[0] = (uint8_t)(v >> 24);
		buf[1] = (uint8_t)(v >> 16);
		buf[2] = (uint8_t)(v >> 8);
		buf[3] = (uint8_t)(v);
}

uint32_t sqlgo_decode_uint32_be(const uint8_t *buf) {
		return ((uint32_t)buf[0] << 24) | ((uint32_t)buf[1] << 16) |
		       ((uint32_t)buf[2] << 8)  | (uint32_t)buf[3];
}

void sqlgo_shift_cell_pointers(
		uint8_t *page_data,
		uint16_t header_size,
		uint16_t from_idx,
		uint16_t to_idx,
		int direction
) {
		if (direction > 0) {
				for (uint16_t i = to_idx; i > from_idx; i--) {
						uint16_t src = header_size + (i - 1) * 2;
						uint16_t dst = header_size + i * 2;
						page_data[dst]     = page_data[src];
						page_data[dst + 1] = page_data[src + 1];
				}
		} else {
				for (uint16_t i = from_idx; i < to_idx; i++) {
						uint16_t src = header_size + (i + 1) * 2;
						uint16_t dst = header_size + i * 2;
						page_data[dst]     = page_data[src];
						page_data[dst + 1] = page_data[src + 1];
				}
		}
}

int sqlgo_encode_varint(uint8_t *buf, uint64_t v) {
		int n = 0;
		do {
				buf[n] = (uint8_t)(v & 0x7F);
				v >>= 7;
				if (v) buf[n] |= 0x80;
				n++;
		} while (v);
		return n;
}

int sqlgo_decode_varint(const uint8_t *buf, uint64_t *out) {
		uint64_t val = 0;
		int shift = 0, n = 0;
		while (1) {
				uint8_t b = buf[n++];
				val |= (uint64_t)(b & 0x7F) << shift;
				if (!(b & 0x80)) break;
				shift += 7;
				if (shift >= 64) break;
		}
		*out = val;
		return n;
}
