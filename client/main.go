package main

import (
	. "KlimoVPiNg/common"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh/terminal"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

type ipParam struct {
	net.IP
}

var _ flag.Value = (*ipParam)(nil)

func (ip *ipParam) Set(s string) error {
	parsed := net.ParseIP(s)
	if parsed == nil {
		return net.InvalidAddrError(s)
	}

	ip.IP = parsed
	return nil
}

func main() {
	addr := &ipParam{}
	addr.IP = net.IPv4(127, 0, 0, 1)

	flag.Var(addr, "a", "IP")
	port := flag.Int("p", 54242, "PORT")
	freq := flag.Duration("f", time.Second*9/8, "FREQUENCY")
	timeout := flag.Duration("t", 5*time.Minute, "TIMEOUT")
	flag.Parse()

	log.SetLevel(log.TraceLevel)
	log.SetOutput(os.Stderr)

	if !terminal.IsTerminal(int(os.Stderr.Fd())) {
		log.SetFormatter(&log.JSONFormatter{DisableHTMLEscape: true})
	}

	raddr := &net.UDPAddr{IP: addr.IP, Port: *port}
	log.WithFields(log.Fields{"addr": raddr.String(), "frequency": *freq, "timeout": *timeout}).Info("pinging")

	client, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		log.WithFields(log.Fields{"addr": addr.String()}).WithError(err).Fatal("can't ping")
	}

	periodically := time.NewTicker(*freq)
	pongs := map[int32]chan time.Time{}
	pongsMtx := &sync.Mutex{}
	out := csv.NewWriter(os.Stdout)
	outMtx := &sync.Mutex{}

	out.Write([]string{"Date (UTC)", "Roundtrip (us)", "Error"})
	go pong(client, pongs, pongsMtx)

	for {
		go ping(<-periodically.C, client, *timeout, pongs, pongsMtx, out, outMtx)
	}
}

func ping(
	tick time.Time, client *net.UDPConn, timeout time.Duration,
	pongs map[int32]chan time.Time, pongsMtx *sync.Mutex, out *csv.Writer, outMtx *sync.Mutex,
) {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	id := rand.Int31()

	enc.SetEscapeHTML(false)

	if err := enc.Encode(&Request{Message{JsonRpcVersion, id}, "v1/ping", map[string]interface{}{}}); err != nil {
		panic(err)
	}

	ch := make(chan time.Time, 1)

	pongsMtx.Lock()
	pongs[id] = ch
	pongsMtx.Unlock()

	defer func() {
		pongsMtx.Lock()
		delete(pongs, id)
		pongsMtx.Unlock()
	}()

	start := time.Now()
	if _, err := client.Write(buf.Bytes()); err != nil {
		log.WithError(err).Error("couldn't ping")
		report(out, outMtx, tick, -timeout, err)
		return
	}

	ctx, cancel := context.WithDeadline(context.Background(), start.Add(timeout))
	defer cancel()

	select {
	case end := <-ch:
		report(out, outMtx, tick, end.Sub(start), nil)
	case <-ctx.Done():
		log.Warn("timeout")
		report(out, outMtx, tick, timeout, ctx.Err())
	}
}

func pong(client *net.UDPConn, pongs map[int32]chan time.Time, pongsMtx *sync.Mutex) {
	for {
		buf := make([]byte, 64*1024)
		l, addr, err := client.ReadFromUDP(buf)
		go onPacket(time.Now(), buf, l, addr, err, pongs, pongsMtx)
	}
}

func report(out *csv.Writer, outMtx *sync.Mutex, tick time.Time, rt time.Duration, err error) {
	row := []string{tick.UTC().Format("2006-01-02 15:04:05"), strconv.FormatInt(int64(rt/time.Microsecond), 10)}
	if err != nil {
		row = append(row, err.Error())
	}

	outMtx.Lock()
	out.Write(row)
	out.Flush()
	outMtx.Unlock()
}

func onPacket(
	ts time.Time, buf []byte, l int, addr *net.UDPAddr, err error, pongs map[int32]chan time.Time, pongsMtx *sync.Mutex,
) {
	if err != nil {
		log.WithFields(log.Fields{"addr": addr.String(), "len": l}).WithError(err).Error("couldn't receive packet")
		return
	}

	resp := &ErrorResponse{}
	if err := json.Unmarshal(buf[:l], resp); err != nil {
		log.WithFields(log.Fields{"addr": addr.String(), "len": l}).WithError(err).Warn("protocol mismatch")
		return
	}

	if resp.JsonRpc != JsonRpcVersion {
		log.WithFields(log.Fields{
			"addr": addr.String(), "expected": JsonRpcVersion, "got": resp.JsonRpc,
		}).Warn("JSON-RPC version mismatch")
		return
	}

	if resp.Id == nil {
		log.WithFields(log.Fields{"addr": addr.String()}).Warn("JSON-RPC message ID missing")
		return
	}

	if resp.Error != nil {
		log.WithFields(log.Fields{"addr": addr.String(), "id": resp.Id, "error": resp.Error}).Warn("JSON-RPC error")
		return
	}

	fid, ok := resp.Id.(float64)
	if !ok {
		log.WithFields(log.Fields{"addr": addr.String(), "id": resp.Id}).Warn("no such JSON-RPC message ID")
		return
	}

	id := int32(fid)
	if float64(id) != fid {
		log.WithFields(log.Fields{"addr": addr.String(), "id": resp.Id}).Warn("no such JSON-RPC message ID")
		return
	}

	pongsMtx.Lock()
	ch := pongs[id]
	pongsMtx.Unlock()

	if ch == nil {
		log.WithFields(log.Fields{"addr": addr.String(), "id": resp.Id}).Warn("no such JSON-RPC message ID")
		return
	}

	ch <- ts
}
