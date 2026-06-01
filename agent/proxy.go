package agent

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

type customDialer struct {
	dialer func(network, addr string) (net.Conn, error)
}

func (c *customDialer) Dial(network, addr string) (net.Conn, error) {
	return c.dialer(network, addr)
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// to make NATS work when using --proxy
func newHTTPConnectDialer(proxyURL *url.URL, forwardDialer *net.Dialer) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		conn, err := forwardDialer.Dial("tcp", proxyURL.Host)
		if err != nil {
			return nil, err
		}

		req := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Opaque: addr},
			Host:   addr,
			Header: make(http.Header),
		}

		if proxyUser := proxyURL.User; proxyUser != nil {
			username := proxyUser.Username()
			password, _ := proxyUser.Password()
			auth := username + ":" + password
			basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
			req.Header.Set("Proxy-Authorization", basicAuth)
		}

		if err := req.Write(conn); err != nil {
			conn.Close()
			return nil, err
		}

		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			conn.Close()
			return nil, err
		}
		resp.Body.Close()

		if resp.StatusCode != 200 {
			conn.Close()
			return nil, fmt.Errorf("newHTTPConnectDialer(): proxy rejected connection: %s", resp.Status)
		}

		return &bufferedConn{Conn: conn, r: br}, nil
	}
}
