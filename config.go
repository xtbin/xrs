// Copyright 2014 Kevin Gillette. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// No reason for this to be longer than 255 bytes -- the protocol won't allow it
// to be filled with more.
var Default = make([]byte, 0, 255)

var Verbs = map[string]Action{
	"show": {1, 1, func(data [][]byte) ([]byte, error) { return data[0], nil }},

	"showdef": {0, 0, func([][]byte) ([]byte, error) { return Default, nil }},
	"setdef": {1, 1,
		func(data [][]byte) ([]byte, error) {
			Default = Default[:len(data[0])]
			copy(Default, data[0])
			return Default, nil
		},
	},
}
