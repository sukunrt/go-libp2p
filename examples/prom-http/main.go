package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	h1, err := libp2p.New(
		libp2p.WithHTTPHandler(promhttp.Handler()),
	)
	if err != nil {
		panic(err)
	}

	h2, err := libp2p.New()
	if err != nil {
		panic(err)
	}
	err = h2.Connect(context.Background(), peer.AddrInfo{Addrs: h1.Addrs(), ID: h1.ID()})
	if err != nil {
		panic(err)
	}

	req, err := http.NewRequest("GET", "/", &bytes.Buffer{})
	if err != nil {
		panic(err)
	}
	s, err := h2.NewStream(context.Background(), h1.ID(), "/libp2p-http")
	req.Write(s)
	resp, err := http.ReadResponse(bufio.NewReader(s), nil)
	if err != nil {
		panic(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(body))
}
