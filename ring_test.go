package main

import "testing"

func PowerOf2(x int) bool {
	return (x-1)&x == 0
}

func TestPowerOf2(t *testing.T) {
	y := PowerOf2(3)
	t.Errorf("d: %v", y)
}

// unsigned nextPowerOf2(unsigned n)
// {
//     // decrement n (to handle the case when n itself
//     // is a power of 2)
//     n = n - 1;

//     // do till only one bit is left
//     while (n & n - 1)
//         n = n & n - 1;    // unset rightmost bit

//     // n is now a power of two (less than n)

//     // return next power of 2
//     return n << 1;
// }

func RoundupPowerOf2(n int) int {
	v := uint32(n)
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return int(v)
}

func TestRoundUp2(t *testing.T) {
	d := RoundupPowerOf2(1025)
	t.Errorf("d: %v", d)
}

func TestRingCount(t *testing.T) {
	tt := []struct {
		r, w               uint
		cnt, space         int
		cntToEnd, spcToEnd int
	}{
		{0, 0, 0, 7, 0, 7},
		{2, 0, 6, 1, 6, 1},
		{4, 0, 4, 3, 4, 3},
		{6, 1, 3, 4, 2, 4},
		{7, 7, 0, 7, 0, 1},
	}
	for _, tc := range tt {
		rr := Ring{buf: make([]byte, 8), r: tc.r, w: tc.w}
		if got := rr.count(); got != tc.cnt {
			t.Fatalf("count([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.cnt)
		}
		if got := rr.free(); got != tc.space {
			t.Fatalf("free([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.space)
		}
		if got := rr.countToEnd(); got != tc.cntToEnd {
			t.Fatalf("countToEnd([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.cntToEnd)
		}
		if got := rr.freeToEnd(); got != tc.spcToEnd {
			t.Fatalf("freeToEnd([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.spcToEnd)
		}
	}
}
