// chisel - A tool to serve fetch, transform, and serve data.
// Copyright (C) 2021 Noel Cower
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog"
)

type ParamKey interface {
	paramKey()
}

type PathKey string

func (PathKey) paramKey() {}

type QueryKey string

func (QueryKey) paramKey() {}

type Params map[ParamKey]interface{}

type Handler struct {
	*EndpointDef
}

func (h *Handler) ParseParams(req *http.Request, pathParams httprouter.Params) Params {
	queryParams := req.URL.Query()
	params := make(Params, len(pathParams)+len(queryParams))
	for k, v := range queryParams {
		params[QueryKey(k)] = v
	}
	for _, entry := range pathParams {
		params[PathKey(entry.Key)] = []string{entry.Value}
	}
	return params
}

func (h *Handler) Get(w http.ResponseWriter, req *http.Request, pathParams httprouter.Params) {
	h.Serve(w, req, h.ParseParams(req, pathParams), nil)
}

func (h *Handler) Post(w http.ResponseWriter, req *http.Request, pathParams httprouter.Params) {
	hct := req.Header.Get("Content-Type")
	if hct == "" {
		hct = "application/json"
	}

	ct, _, err := mime.ParseMediaType(hct)
	if err != nil {
		zerolog.Ctx(req.Context()).Debug().
			Err(err).
			Str("content_type", hct).
			Msg("Failed to parse media type for request.")
	}

	var body interface{}
	switch ct {
	case "application/x-www-form-urlencoded":
		if pe := req.ParseForm(); pe != nil {
		}
	case "application/json":
		data, re := io.ReadAll(req.Body)
		if re != nil {
			http.Error(w, "error reading request body", http.StatusNotAcceptable)
			return
		}
		if je := json.Unmarshal(data, &body); je != nil {
			http.Error(w, "error parsing request body", http.StatusNotAcceptable)
			return
		}
	default:
		data, re := io.ReadAll(req.Body)
		if re != nil {
			http.Error(w, "error reading request body", http.StatusNotAcceptable)
			return
		}
		body = string(data)
	}

	params := h.ParseParams(req, pathParams)

	h.Serve(w, req, params, body)
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, params Params, body interface{}) {
}
