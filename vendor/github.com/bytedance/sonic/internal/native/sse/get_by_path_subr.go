// +build !noasm !appengine
// Code generated by asm2asm, DO NOT EDIT.

package sse

import (
	`github.com/bytedance/sonic/loader`
)

const (
    _entry__get_by_path = 224
)

const (
    _stack__get_by_path = 216
)

const (
    _size__get_by_path = 22168
)

var (
    _pcsp__get_by_path = [][2]uint32{
        {1, 0},
        {4, 8},
        {6, 16},
        {8, 24},
        {10, 32},
        {12, 40},
        {13, 48},
        {12658, 216},
        {12665, 48},
        {12666, 40},
        {12668, 32},
        {12670, 24},
        {12672, 16},
        {12674, 8},
        {12675, 0},
        {22168, 216},
    }
)

var _cfunc_get_by_path = []loader.CFunc{
    {"_get_by_path_entry", 0,  _entry__get_by_path, 0, nil},
    {"_get_by_path", _entry__get_by_path, _size__get_by_path, _stack__get_by_path, _pcsp__get_by_path},
}
