// chisel - A tool to fetch, transform, and serve data.
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
	"fmt"
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

func (p Params) MarshalJSON() ([]byte, error) {
	m := struct {
		Path  map[string]interface{}
		Query map[string]interface{}
	}{
		Path:  map[string]interface{}{},
		Query: map[string]interface{}{},
	}
	for k, v := range p {
		switch k := k.(type) {
		case PathKey:
			m.Path[string(k)] = v
		case QueryKey:
			m.Query[string(k)] = v
		}
	}
	return json.Marshal(m)
}

type Handler struct {
	*EndpointDef
}

func (h *Handler) ParseParams(req *http.Request, pathParams httprouter.Params) (Params, error) {
	ctx := req.Context()
	queryParams := req.URL.Query()
	params := make(Params, len(pathParams)+len(queryParams))
	for k, v := range queryParams {
		vi := make([]interface{}, len(v))
		for i, s := range v {
			vi[i] = s
		}
		params[QueryKey(k)] = vi
	}
	for _, entry := range pathParams {
		params[PathKey(entry.Key)] = entry.Value
	}

	for k, pd := range h.PathParams {
		k := PathKey(k)
		v, ok := params[k]
		if !ok {
			continue
		}
		v, err := pd.Map.Apply(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("error mapping path parameter %q: %w", k, err)
		}
		params[k] = v
	}

	for k, pd := range h.QueryParams {
		k := QueryKey(k)
		v, ok := params[k]
		if !ok {
			continue
		}
		v, err := pd.Map.Apply(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("error mapping query parameter %q: %w", k, err)
		}
		params[k] = v
	}

	return params, nil
}

func (h *Handler) Get(w http.ResponseWriter, req *http.Request, pathParams httprouter.Params) {
	params, err := h.ParseParams(req, pathParams)
	if err != nil {
		zerolog.Ctx(req.Context()).Error().
			Err(err).
			Msg("Error parsing parameters. Request aborted.")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.Serve(w, req, params, nil)
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
		if len(data) == 0 {
			break
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
		if len(data) == 0 {
			break
		}
		body = string(data)
	}

	params, err := h.ParseParams(req, pathParams)
	if err != nil {
		zerolog.Ctx(req.Context()).Error().
			Err(err).
			Msg("Error parsing parameters. Request aborted.")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.Serve(w, req, params, body)
}

func (h *Handler) Serve(w http.ResponseWriter, req *http.Request, params Params, body interface{}) {
	// TODO: Pass body, params through endpoint transaction stages.
}
