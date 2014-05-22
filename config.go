// Copyright 2014 Kevin Gillette. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"os"
	"strings"
)

func sendone(ch chan<- Message, msg Message) error {
	ch <- msg
	close(ch)
	return nil
}

var Actions = map[string]Action{
	"print": func(ch chan<- Message, args []string) error {
		return sendone(ch, Message{Data: []byte(strings.Join(args, " "))})
	},

	"pipe": func(ch chan<- Message, args []string) error {
		s := bufio.NewScanner(os.Stdin)
		for s.Scan() {
			buf := s.Bytes()
			if len(buf) == 0 {
				continue
			}
			ch <- Message{Data: buf}
		}
		close(ch)
		return s.Err()
	},
}
