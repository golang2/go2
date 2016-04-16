// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"unsafe"
)

type slice struct {
	array unsafe.Pointer
	len   int
	cap   int
}

// TODO: take uintptrs instead of int64s?
func makeslice(t *slicetype, len64, cap64 int64) slice {
	// NOTE: The len > maxElements check here is not strictly necessary,
	// but it produces a 'len out of range' error instead of a 'cap out of range' error
	// when someone does make([]T, bignumber). 'cap out of range' is true too,
	// but since the cap is only being supplied implicitly, saying len is clearer.
	// See issue 4085.

	maxElements := ^uintptr(0)
	if t.elem.size > 0 {
		maxElements = _MaxMem / t.elem.size
	}

	len := int(len64)
	if len64 < 0 || int64(len) != len64 || uintptr(len) > maxElements {
		panic(errorString("makeslice: len out of range"))
	}

	cap := int(cap64)
	if cap < len || int64(cap) != cap64 || uintptr(cap) > maxElements {
		panic(errorString("makeslice: cap out of range"))
	}

	p := newarray(t.elem, uintptr(cap))
	return slice{p, len, cap}
}

// growslice handles slice growth during append.
// It is passed the slice type, the old slice, and the desired new minimum capacity,
// and it returns a new slice with at least that capacity, with the old data
// copied into it.
// The new slice's length is set to the old slice's length,
// NOT to the new requested capacity.
// This is for codegen convenience. The old slice's length is used immediately
// to calculate where to write new values during an append.
// TODO: When the old backend is gone, reconsider this decision.
// The SSA backend might prefer the new length or to return only ptr/cap and save stack space.
func growslice(t *slicetype, old slice, cap int) slice {
	if raceenabled {
		callerpc := getcallerpc(unsafe.Pointer(&t))
		racereadrangepc(old.array, uintptr(old.len*int(t.elem.size)), callerpc, funcPC(growslice))
	}
	if msanenabled {
		msanread(old.array, uintptr(old.len*int(t.elem.size)))
	}

	et := t.elem
	if et.size == 0 {
		if cap < old.cap {
			panic(errorString("growslice: cap out of range"))
		}
		// append should not create a slice with nil pointer but non-zero len.
		// We assume that append doesn't need to preserve old.array in this case.
		return slice{unsafe.Pointer(&zerobase), old.len, cap}
	}

	newcap := old.cap
	doublecap := newcap + newcap
	if cap > doublecap {
		newcap = cap
	} else {
		if old.len < 1024 {
			newcap = doublecap
		} else {
			for newcap < cap {
				newcap += newcap / 4
			}
		}
	}

	var lenmem, capmem, maxcap uintptr
	const ptrSize = unsafe.Sizeof((*byte)(nil))
	switch et.size {
	case 1:
		lenmem = uintptr(old.len)
		capmem = roundupsize(uintptr(newcap))
		newcap = int(capmem)
		maxcap = _MaxMem
	case ptrSize:
		lenmem = uintptr(old.len) * ptrSize
		capmem = roundupsize(uintptr(newcap) * ptrSize)
		newcap = int(capmem / ptrSize)
		maxcap = _MaxMem / ptrSize
	default:
		lenmem = uintptr(old.len) * et.size
		capmem = roundupsize(uintptr(newcap) * et.size)
		newcap = int(capmem / et.size)
		maxcap = _MaxMem / et.size
	}

	if cap < old.cap || uintptr(newcap) > maxcap {
		panic(errorString("growslice: cap out of range"))
	}

	var p unsafe.Pointer
	if et.kind&kindNoPointers != 0 {
		p = rawmem(capmem)
		memmove(p, old.array, lenmem)
		memclr(add(p, lenmem), capmem-lenmem)
	} else {
		// Note: can't use rawmem (which avoids zeroing of memory), because then GC can scan uninitialized memory.
		p = newarray(et, uintptr(newcap))
		if !writeBarrier.enabled {
			memmove(p, old.array, lenmem)
		} else {
			for i := uintptr(0); i < lenmem; i += et.size {
				typedmemmove(et, add(p, i), add(old.array, i))
			}
		}
	}

	return slice{p, old.len, newcap}
}

func slicecopy(to, fm slice, width uintptr) int {
	if fm.len == 0 || to.len == 0 {
		return 0
	}

	n := fm.len
	if to.len < n {
		n = to.len
	}

	if width == 0 {
		return n
	}

	if raceenabled {
		callerpc := getcallerpc(unsafe.Pointer(&to))
		pc := funcPC(slicecopy)
		racewriterangepc(to.array, uintptr(n*int(width)), callerpc, pc)
		racereadrangepc(fm.array, uintptr(n*int(width)), callerpc, pc)
	}
	if msanenabled {
		msanwrite(to.array, uintptr(n*int(width)))
		msanread(fm.array, uintptr(n*int(width)))
	}

	size := uintptr(n) * width
	if size == 1 { // common case worth about 2x to do here
		// TODO: is this still worth it with new memmove impl?
		*(*byte)(to.array) = *(*byte)(fm.array) // known to be a byte pointer
	} else {
		memmove(to.array, fm.array, size)
	}
	return n
}

func slicestringcopy(to []byte, fm string) int {
	if len(fm) == 0 || len(to) == 0 {
		return 0
	}

	n := len(fm)
	if len(to) < n {
		n = len(to)
	}

	if raceenabled {
		callerpc := getcallerpc(unsafe.Pointer(&to))
		pc := funcPC(slicestringcopy)
		racewriterangepc(unsafe.Pointer(&to[0]), uintptr(n), callerpc, pc)
	}
	if msanenabled {
		msanwrite(unsafe.Pointer(&to[0]), uintptr(n))
	}

	memmove(unsafe.Pointer(&to[0]), stringStructOf(&fm).str, uintptr(n))
	return n
}
