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
	"sort"
	"syscall"
	"time"

	"github.com/BurntSushi/xgbutil"
	"github.com/BurntSushi/xgbutil/xprop"
)

type Action func(ch chan<- Message, args []string) error

type Flag uint64

func (f Flag) Is(v Flag) bool { return f&v != 0 }

const (
	FlagSetDefault Flag = 1 << iota // implies FlagPersist
	FlagPersist
)

type Message struct {
	Data []byte
	Flag
}

const MaxMsgSize = 255

var (
	DataBuf = make([]byte, 2+MaxMsgSize)
	Default = make([]byte, 0, MaxMsgSize)
	MsgCh   = make(chan Message)
	Display string
	DispDur time.Duration
	LineDur time.Duration
	CharDur time.Duration

	Serve      bool
	Persist    bool
	SetDefault bool
	Prefix     bool // strip input to prefix or suffix;
	Suffix     bool // default is to consider overflows an error

	ErrMsgTooLarge = errors.New("data elements must contain no more than 255 bytes each")
)

func main() {
	log.SetFlags(0)

	flag.StringVar(&Display, "display", os.Getenv("DISPLAY"), "")
	flag.BoolVar(&Serve, "s", false, "run server")
	flag.BoolVar(&SetDefault, "d", false, "set default message (client)")
	flag.BoolVar(&Persist, "p", false, "persist message (client)")
	flag.BoolVar(&Prefix, "P", false, "strip to prefix")
	flag.BoolVar(&Suffix, "S", false, "strip to suffix")
	flag.DurationVar(&DispDur, "t", 10*time.Second, "display duration (server)")
	flag.DurationVar(&LineDur, "l", 1*time.Second, "minimum line duration (client pipe)")
	flag.DurationVar(&CharDur, "c", 60*time.Millisecond, "line duration per character (client pipe)")
	flag.Parse()

	if Prefix && Suffix {
		log.Fatalln("only one of -P or -S may be specified")
	}
	if Display == "" {
		log.Fatalln("empty DISPLAY")
	}
	u, err := user.Current()
	if err != nil {
		log.Fatalln("user lookup error:", err)
	}
	username := u.Username
	if username == "" {
		username = u.Uid
	}
	socket := filepath.Join(os.TempDir(), "xrs."+username, Display)
	if Serve {
		Server(socket)
	} else {
		Client(socket)
	}
}

func Server(socket string) {
	dir := filepath.Dir(socket)
	err := os.Mkdir(dir, 0750)
	if err != nil && !os.IsExist(err) {
		log.Fatalln("socket directory creation error:", err)
	}
	defer os.Remove(dir)
	x, err := xgbutil.NewConnDisplay(Display)
	if err != nil {
		log.Fatalln("X connection error:", err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		log.Fatalln("listen error:", err)
	}
	defer l.Close()
	var (
		timer Timer
		msg   Message
		buf   []byte
		tch   <-chan time.Time
		sig   = make(chan os.Signal)
	)
	SetStatus(x, Default)
	log.SetFlags(log.LstdFlags)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				log.Println("connection error:", err)
				continue
			}
			t := time.Now().Add(10 * time.Millisecond)
			c.SetReadDeadline(t)
			msg, err := Decode(c)
			c.Close()
			if err != nil {
				log.Println("decode error:", err)
				continue
			}
			MsgCh <- msg
		}
	}()
	if DispDur == 0 {
		timer = NopTimer{}
	} else {
		t := time.NewTimer(0)
		t.Stop()
		tch = t.C
		timer = t
	}
	for {
		select {
		case <-sig:
			return
		case <-tch:
			buf = Default
		case msg = <-MsgCh:
			buf = msg.Data
			if msg.Is(FlagSetDefault) {
				Default = append(Default[:0], buf...)
			} else if len(buf) == 0 {
				buf = Default
			} else if !msg.Is(FlagPersist) {
				timer.Reset(DispDur)
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
	if verb == "" || verb == "help" {
		ListCommands()
		return
	}
	action, ok := Actions[verb]
	if !ok {
		log.Fatalf("unrecognized action %q", verb)
	}
	go func() {
		err := action(MsgCh, flag.Args()[1:])
		if err != nil {
			log.Fatalln(err)
		}
	}()
	var dur time.Duration
	for msg := range MsgCh {
		var err error
		if dur > 0 {
			time.Sleep(dur)
		}
		switch {
		case SetDefault:
			msg.Flag |= FlagSetDefault
			fallthrough
		case Persist:
			msg.Flag |= FlagPersist
		}
		msg.Data, err = PrepareBuf(msg.Data)
		if err != nil {
			log.Fatalln(err)
		}
		c, err := net.Dial("unix", socket)
		if err != nil {
			log.Fatalln("connection error:", err)
		}
		err = Encode(c, msg)
		c.Close()
		if err != nil {
			log.Fatalln("encode error:", err)
		}
		dur = CharDur * time.Duration(len(msg.Data))
		if dur < LineDur {
			dur = LineDur
		}
	}
}

func ListCommands() {
	lst := make([]string, 0, len(Actions))
	for verb := range Actions {
		lst = append(lst, verb)
	}
	sort.Strings(lst)
	for _, verb := range lst {
		fmt.Println(verb)
	}
}

func PrepareBuf(p []byte) ([]byte, error) {
	if len(p) <= MaxMsgSize {
		return p, nil
	} else if Prefix {
		return p[:MaxMsgSize], nil
	} else if Suffix {
		return p[len(p)-MaxMsgSize:], nil
	}
	return nil, ErrMsgTooLarge
}

func Encode(w io.Writer, msg Message) error {
	if len(msg.Data) > MaxMsgSize {
		return ErrMsgTooLarge
	}
	buf := DataBuf[:1+len(msg.Data)]
	buf[0] = byte(msg.Flag)
	copy(buf[1:], msg.Data)
	_, err := w.Write(buf)
	return err
}

func Decode(r io.Reader) (msg Message, err error) {
	n, err := io.ReadFull(r, DataBuf)
	if err == nil {
		return msg, ErrMsgTooLarge
	} else if err != io.ErrUnexpectedEOF {
		return msg, err
	}
	buf := DataBuf[:n]
	msg.Flag = Flag(buf[0])
	msg.Data = buf[1:]
	return msg, nil
}

type Timer interface {
	Reset(time.Duration) bool
	Stop() bool
}

type NopTimer struct{}

func (n NopTimer) Reset(time.Duration) bool { return true }
func (n NopTimer) Stop() bool               { return true }

type NumArgsError struct {
	Name     string
	Min, Max int
}

func (e NumArgsError) Error() string {
	return fmt.Sprint(e.Name, "accepts between", e.Min, "and", e.Max, "arguments")
}
