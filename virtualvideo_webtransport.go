package kvm

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/quic-go/quic-go/http3"
	webtransport "github.com/quic-go/webtransport-go"
)

const virtualVideoWebTransportAddr = ":9443"

var virtualVideoWebTransportServer *webtransport.Server

func RunVirtualVideoWebTransportServer() {
	if config.TLSMode == "" {
		logger.Info().Msg("WebTransport virtual video server disabled because TLS is disabled")
		return
	}

	mux := http.NewServeMux()
	virtualVideoWebTransportServer = &webtransport.Server{
		H3: &http3.Server{
			Addr: virtualVideoWebTransportAddr,
			TLSConfig: &tls.Config{
				GetCertificate: getCertificate,
				MinVersion:     tls.VersionTLS12,
			},
			Handler: mux,
		},
		CheckOrigin: sameHostOrigin,
	}

	mux.HandleFunc("/virtual-video/webtransport", handleVirtualVideoWebTransport)

	logger.Info().Str("addr", virtualVideoWebTransportAddr).Msg("starting virtual video WebTransport server")
	if err := virtualVideoWebTransportServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Warn().Err(err).Msg("virtual video WebTransport server stopped")
	}
}

func sameHostOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost, _, err := net.SplitHostPort(originURL.Host)
	if err != nil {
		originHost = originURL.Host
	}
	requestHost, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		requestHost = r.Host
	}
	return originHost == requestHost
}

func handleVirtualVideoWebTransport(w http.ResponseWriter, r *http.Request) {
	if virtualVideoWebTransportServer == nil {
		http.Error(w, "webtransport server unavailable", http.StatusServiceUnavailable)
		return
	}
	session, err := virtualVideoWebTransportServer.Upgrade(w, r)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to upgrade virtual video WebTransport session")
		return
	}
	go handleVirtualVideoWebTransportSession(session)
}

func handleVirtualVideoWebTransportSession(session *webtransport.Session) {
	ctx, cancel := context.WithCancel(session.Context())
	defer cancel()
	defer session.CloseWithError(0, "closed")

	for {
		stream, err := session.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		_ = stream.SetReadDeadline(time.Now().Add(5 * time.Second))
		data, err := io.ReadAll(stream)
		if err != nil {
			logger.Warn().Err(err).Msg("failed reading virtual video WebTransport stream")
			continue
		}
		if virtualVideo == nil {
			logger.Warn().Msg("virtual video manager is not initialized")
			continue
		}
		if err := virtualVideo.HandleFrameData(data, "webtransport-quic"); err != nil {
			logger.Warn().Err(fmt.Errorf("remote=%s: %w", session.RemoteAddr(), err)).Msg("failed to handle WebTransport virtual video frame")
		}
	}
}
