// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package api

import (
	"net/http"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/summarize"
)

// Summaries of recordings (ARCHITECTURE.md "Summaries"). Reading a summary is like reading the
// recording: the owner and this machine's agents (MCP recording_summary); asking for a new one
// and the settings are the owner's. Without the service every route answers 501.
func (s *Server) summaryRoutes(mux *http.ServeMux) {
	o := func(h http.HandlerFunc) http.HandlerFunc { return s.ownerOnly(s.withSummaries501(h)) }
	mux.HandleFunc("GET /api/recordings/{id}/summary", s.ownerOrAgent(s.withSummaries501(s.getSummary)))
	mux.HandleFunc("POST /api/recordings/{id}/summarize", o(s.summarizeRecording))
	mux.HandleFunc("GET /api/summaries/settings", o(s.getSummarySettings))
	mux.HandleFunc("PUT /api/summaries/settings", o(s.putSummarySettings))
}

func (s *Server) withSummaries501(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sum == nil {
			writeError(w, http.StatusNotImplemented, "summaries are not available on this daemon")
			return
		}
		h(w, r)
	}
}

// withSummaries fills the recordings' summary and summary_state (lists show the first sentence).
func (s *Server) withSummaries(list []model.Recording) {
	if s.sum != nil {
		s.sum.Attach(list)
	}
}

func (s *Server) withSummary(r model.Recording) model.Recording {
	list := []model.Recording{r}
	s.withSummaries(list)
	return list[0]
}

// GET /api/recordings/{id}/summary → RecordingSummary (state "none" when there is none yet).
func (s *Server) getSummary(w http.ResponseWriter, r *http.Request) {
	x, err := s.sum.Get(r.PathValue("id"))
	if err != nil {
		writeRecErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, x)
}

// POST /api/recordings/{id}/summarize → 202 RecordingSummary: transcribe what is missing, then
// write the summary again (Regenerate). 409 while the recording runs or Claude writes this one.
func (s *Server) summarizeRecording(w http.ResponseWriter, r *http.Request) {
	x, err := s.sum.Summarize(r.PathValue("id"))
	if err != nil {
		writeRecErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, x)
}

func (s *Server) getSummarySettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sum.Settings())
}

// PUT /api/summaries/settings {auto?, model?}
func (s *Server) putSummarySettings(w http.ResponseWriter, r *http.Request) {
	var req summarize.SettingsPatch
	if !decode(w, r, &req) {
		return
	}
	st, err := s.sum.Configure(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
