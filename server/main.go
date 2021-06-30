package main

import (
	. "KlimoVPiNg/common"
	"bytes"
	"encoding/json"
	"flag"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh/terminal"
	"net"
	"os"
)

func main() {
	port := flag.Int("p", 54242, "PORT")
	flag.Parse()

	log.SetLevel(log.TraceLevel)
	log.SetOutput(os.Stdout)

	if !terminal.IsTerminal(int(os.Stdout.Fd())) {
		log.SetFormatter(&log.JSONFormatter{DisableHTMLEscape: true})
	}

	addr := &net.UDPAddr{IP: net.IPv6zero, Port: *port}
	log.WithFields(log.Fields{"addr": addr.String()}).Info("listening")

	server, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.WithFields(log.Fields{"addr": addr.String()}).WithError(err).Fatal("can't listen")
	}

	for {
		buf := make([]byte, 64*1024)
		l, addr, err := server.ReadFromUDP(buf)
		go onPacket(server, buf, l, addr, err)
	}
}

func onPacket(server *net.UDPConn, buf []byte, l int, addr *net.UDPAddr, err error) {
	if err != nil {
		log.WithFields(log.Fields{"addr": addr.String(), "len": l}).WithError(err).Error("couldn't receive packet")
		return
	}

	req := &Request{}
	if err := json.Unmarshal(buf[:l], req); err != nil {
		log.WithFields(log.Fields{"addr": addr.String(), "len": l}).WithError(err).Warn("protocol mismatch")
		return
	}

	if req.JsonRpc != JsonRpcVersion {
		log.WithFields(log.Fields{
			"addr": addr.String(), "expected": JsonRpcVersion, "got": req.JsonRpc,
		}).Warn("JSON-RPC version mismatch")
		return
	}

	var resp interface{}
	var noIdSeverity log.Level

	switch req.Method {
	case "v1/ping":
		resp = &SuccessResponse{req.Message, req.Params}
		noIdSeverity = log.WarnLevel
	default:
		log.WithFields(log.Fields{
			"addr": addr.String(), "expected": []string{"v1/ping"}, "got": req.Method,
		}).Warn("bad JSON-RPC method")

		resp = &ErrorResponse{req.Message, "bad method"}
		noIdSeverity = log.InfoLevel
	}

	if req.Id == nil {
		log.WithFields(log.Fields{
			"addr": addr.String(), "method": req.Method,
		}).Log(noIdSeverity, "no message ID - not responding")
	} else {
		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)

		enc.SetEscapeHTML(false)

		if err := enc.Encode(resp); err != nil {
			panic(err)
		}

		if _, err := server.WriteToUDP(buf.Bytes(), addr); err == nil {
			if _, ok := resp.(*SuccessResponse); ok {
				log.WithFields(log.Fields{
					"addr": addr.String(), "method": req.Method, "id": req.Id,
				}).Trace("responded")
			}
		} else {
			log.WithFields(log.Fields{
				"addr": addr.String(), "method": req.Method, "id": req.Id,
			}).WithError(err).Log(noIdSeverity, "couldn't respond")
		}
	}
}
