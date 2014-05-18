// Copyright 2014 Kevin Gillette. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"time"

	"github.com/BurntSushi/xgbutil"
	"github.com/BurntSushi/xgbutil/xprop"
)

var (
	Display string
	Reset   time.Duration
	MsgCh   chan []byte
)

func main() {
	var (
		display string
		serve   bool
	)
	flag.StringVar(&Display, "display", os.Getenv("DISPLAY"), "")
	flag.BoolVar(&serve, "s", false, "run server")
	flag.DurationVar(&Reset, "r", 10*time.Second, "display duration")
	flag.Parse()

	if Display == "" {
		log.Fatalln("empty DISPLAY")
	}

	u, err := user.Current()
	if err != nil {
		log.Fatalln("user lookup error:", err)
	}
	socket := filepath.Join(os.TempDir(), "xrs."+u.Username, display)
	if serve {
		Server(socket)
	} else {
		Client(socket)
	}
}

func Server(socket string) {
	err := os.Mkdir(filepath.Dir(socket), 0750)
	if err != nil && !os.IsExist(err) {
		log.Fatalln("socket directory creation error:", err)
	}
	x, err := xgbutil.NewConnDisplay(Display)
	if err != nil {
		log.Fatalln("X connection error:", err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		log.Fatalln("listen error:", err)
	}
	var (
		timer Timer
		buf   []byte
		tch   <-chan time.Time
		sig   = make(chan os.Signal)
		//quit  = make(chan struct{})
	)
	MsgCh = make(chan []byte)
	signal.Notify(sig, os.Interrupt)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				log.Println("connection error:", err)
				continue
			}
			t := time.Now().Add(100 * time.Millisecond)
			c.SetReadDeadline(t)
			data, err := Decode(c)
			c.Close()
			if err != nil {
				log.Println("decode error:", err)
				continue
			}
			verb, data := data[0], data[1:]
			action, ok := Verbs[string(verb)]
			if !ok {
				log.Printf("unrecognized action %q", verb)
				continue
			}
			if len(data) < action.L || len(data) > action.H {
				log.Printf("%s takes [%d,%d] arguments", verb, action.L, action.H)
				continue
			}
			out, err := action.F(data)
			if err != nil {
				log.Printf("%s execution error: %v", verb, err)
				continue
			}
			MsgCh <- out
		}
	}()
	if Reset == 0 {
		timer = NopTimer{}
	} else {
		t := time.NewTimer(0)
		t.Stop()
		tch = t.C
		timer = t
	}
loop:
	for {
		select {
		case <-sig:
			l.Close()
			break loop
		case <-tch:
			buf = Default
		case buf = <-MsgCh:
			if &buf[0] != &Default[0] {
				timer.Reset(Reset)
			}
		}
		err := SetStatus(x, buf)
		if err != nil {
			log.Println("status set error:", err)
		}
	}
}

func SetStatus(x *xgbutil.XUtil, b []byte) error {
	return xprop.ChangeProp(x, x.RootWin(), 8, "WM_NAME", "STRING", b)
}

func Client(socket string) {
	verb := flag.Arg(0)
	action, ok := Verbs[verb]
	if !ok {
		log.Fatalf("unrecognized verb %q", verb)
	}
	n := flag.NArg() - 1
	if n < action.L || action.H < n {
		log.Fatalf("%s takes [%d,%d] arguments", verb, action.L, action.H)
	}
	c, err := net.Dial("unix", socket)
	if err != nil {
		log.Fatalln("connection error:", err)
	}
	err = Encode(c, flag.Args())
	if err != nil {
		log.Fatalln("encode error:", err)
	}
	c.Close()
}

const MaxElems = 15 // must not be more than 255

var (
	ErrTooManyElems = fmt.Errorf("data must contain no more than %d elements", MaxElems)
	ErrNoElems      = errors.New("data must contain at least one element")
	ErrElemTooLarge = errors.New("data elements must contain no more than 255 bytes each")
	ErrBufOverflow  = errors.New("buffer overflow")
)

var (
	Buf  = make([]byte, 1+MaxElems*256)
	Data = make([][]byte, MaxElems)
)

func Encode(w io.Writer, data []string) error {
	if len(data) > 15 {
		return ErrTooManyElems
	}
	Buf[0] = byte(len(data))
	i := 1
	for _, s := range data {
		if len(s) > 255 {
			return ErrElemTooLarge
		}
		Buf[i] = byte(len(s))
		copy(Buf[i+1:], s)
		i += 1 + len(s)
	}
	_, err := w.Write(Buf[:i])
	return err
}

func Decode(r io.Reader) ([][]byte, error) {
	n, err := io.ReadFull(r, Buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	size := int(Buf[0])
	if size == 0 {
		return nil, ErrNoElems
	}
	if size > MaxElems {
		return nil, ErrTooManyElems
	}
	data := Data[:size]
	i := 1
	for idx := range data {
		j := i + 1 + int(Buf[i])
		if j > n {
			return nil, ErrBufOverflow
		}
		data[idx] = Buf[i+1 : j]
		i = j
	}
	return data, nil
}

type Timer interface {
	Reset(time.Duration) bool
	Stop() bool
}

type NopTimer struct{}

func (n NopTimer) Reset(time.Duration) bool { return true }
func (n NopTimer) Stop() bool               { return true }

type Action struct {
	L int
	H int
	F func([][]byte) ([]byte, error)
}
