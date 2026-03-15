package api

import (
	"net/http"

	"github.com/BenRachmiel/preamp/internal/scanner"
)

// SetScanner wires up the scanner after server creation (avoids circular init).
func (s *Server) SetScanner(sc *scanner.Scanner) {
	s.scanner = sc
}

func (s *Server) handleStartScan(w http.ResponseWriter, r *http.Request) {
	if s.scanner == nil {
		writeError(w, r, 0, "scanner not configured")
		return
	}

	go func() {
		if err := s.scanner.Run(); err != nil {
			s.log.Error("scan failed", "err", err)
		}
	}()

	resp := ok()
	resp.ScanStatus = &ScanStatus{Scanning: true}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetScanStatus(w http.ResponseWriter, r *http.Request) {
	resp := ok()
	if s.scanner != nil {
		resp.ScanStatus = &ScanStatus{
			Scanning: s.scanner.Scanning(),
			Count:    s.scanner.Count(),
		}
	} else {
		resp.ScanStatus = &ScanStatus{Scanning: false}
	}
	writeResponse(w, r, resp)
}
