// Copyright 2014 Kevin Gillette. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func init() { Default = append(Default, "xrs"...) }

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

	"date": func(ch chan<- Message, args []string) error {
		s := time.Now().Format("Mon Jan 2 15:04")
		return sendone(ch, Message{Data: []byte(s)})
	},

	// displays: <status> <current-mAh> <percent-charge> <max-mAh> <percent-max-of-factory> <factory-max-mAh>
	"battery": func(ch chan<- Message, args []string) error {
		const base = "/sys/class/power_supply/BAT0"
		files := []string{"charge_now", "charge_full", "charge_full_design", "status"}
		for i, fn := range files {
			b, err := ioutil.ReadFile(filepath.Join(base, fn))
			if err != nil {
				return err
			}
			files[i] = string(b)
		}
		vals := make([]int, 3)
		for i := range vals {
			v, err := strconv.Atoi(strings.TrimSpace(files[i]))
			if err != nil {
				return err
			}
			vals[i] = v / 1000
		}
		status := strings.TrimSpace(files[3])
		cnow, cmax, cfac := vals[0], vals[1], vals[2]
		pnow, pfac := 100*cnow/cmax, 100*cmax/cfac
		s := fmt.Sprintf("%s %d %d%% %d %d%% %d", status, cnow, pnow, cmax, pfac, cfac)
		return sendone(ch, Message{Data: []byte(s)})
	},
}
