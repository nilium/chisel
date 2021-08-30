// chisel - A tool to fetch, transform, and serve data.
// Copyright 2021 Noel Cower
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strconv"

	"github.com/jmoiron/sqlx"
	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog"
	"go.spiff.io/sql/vdb"
)

type Params struct {
	Path  map[string]interface{} `json:"path"`
	Query map[string]interface{} `json:"query"`
}

func newParams(pathCap, queryCap int) *Params {
	return &Params{
		Path:  make(map[string]interface{}, pathCap),
		Query: make(map[string]interface{}, queryCap),
	}
}

func (p *Params) Opaque() map[string]interface{} {
	return map[string]interface{}{
		"path":  p.Path,
		"query": p.Query,
	}
}

type Handler struct {
	*EndpointDef

	db map[string]*Database
}

func (h *Handler) ParseParams(req *http.Request, pathParams httprouter.Params) (*Params, error) {
	ctx := req.Context()
	queryParams := req.URL.Query()
	params := newParams(len(pathParams), len(queryParams))
	for k, v := range queryParams {
		vi := make([]interface{}, len(v))
		for i, s := range v {
			vi[i] = s
		}
		params.Query[k] = vi
	}
	for _, entry := range pathParams {
		params.Path[entry.Key] = entry.Value
	}

	mapParams := func(mappings ParamMappings, params map[string]interface{}) error {
		for k, pd := range mappings {
			v, ok := params[k]
			if !ok {
				continue
			}
			v, err := pd.Map.Apply(ctx, v, nil)
			if err != nil {
				return fmt.Errorf("error mapping parameter %q: %w", k, err)
			}
			params[k] = v
		}
		return nil
	}

	if err := mapParams(h.PathParams, params.Path); err != nil {
		return nil, fmt.Errorf("failed to map path parameters: %w", err)
	}

	if err := mapParams(h.QueryParams, params.Query); err != nil {
		return nil, fmt.Errorf("failed to map query parameters: %w", err)
	}

	return params, nil
}

func (h *Handler) WithLogger(req *http.Request) (*http.Request, context.Context, zerolog.Logger) {
	ctx := req.Context()
	log := zerolog.Ctx(ctx).With().
		Str("method", h.Method).
		Str("path", h.Path).
		Str("url", req.URL.Redacted()).
		Str("ua", req.UserAgent()).
		Str("raddr", req.RemoteAddr).
		Logger()
	ctx = log.WithContext(ctx)
	return req.WithContext(ctx), ctx, log
}

func (h *Handler) Get(w http.ResponseWriter, req *http.Request, pathParams httprouter.Params) {
	req, ctx, log := h.WithLogger(req)

	params, err := h.ParseParams(req, pathParams)
	if err != nil {
		log.Trace().Err(err).Msg("Error parsing parameters. Request aborted.")
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	out, err := h.computeResponse(ctx, log, w, req, params, nil)
	if err != nil {
		return
	}
	h.reply(ctx, log, w, out)
}

func (h *Handler) Post(w http.ResponseWriter, req *http.Request, pathParams httprouter.Params) {
	req, ctx, log := h.WithLogger(req)

	var body interface{}
	switch h.BodyType {
	case FormBodyType:
		if pe := req.ParseForm(); pe != nil {
			// TODO: Assign parsed form to body as
			// map[string]interface{} (for gojq).
		}
	case JSONBodyType:
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
	case StringBodyType:
		data, re := io.ReadAll(req.Body)
		if re != nil {
			http.Error(w, "error reading request body", http.StatusNotAcceptable)
			return
		}
		if len(data) == 0 {
			break
		}
		body = string(data)
	case NoBodyType:
		// Nop.
	}

	params, err := h.ParseParams(req, pathParams)
	if err != nil {
		zerolog.Ctx(req.Context()).Error().
			Err(err).
			Msg("Error parsing parameters. Request aborted.")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	out, err := h.computeResponse(ctx, log, w, req, params, body)
	if err != nil {
		return
	}
	h.reply(ctx, log, w, out)
}

func opaqueInt(v interface{}) (int64, bool) {
	switch v := v.(type) {
	case nil:
		return 0, false
	case float64:
		return int64(v), true
	case *big.Int:
		return v.Int64(), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case string:
		i, err := strconv.ParseInt(v, 0, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func opaqueStrings(v interface{}) ([]string, bool) {
	switch v := v.(type) {
	case nil:
		return nil, false
	case []interface{}:
		strs := make([]string, 0, len(v))
		for _, iv := range v {
			if s, ok := opaqueString(iv); ok {
				strs = append(strs, s)
			}
		}
		return strs, len(strs) > 0
	default:
		s, ok := opaqueString(v)
		if !ok {
			return nil, false
		}
		return []string{s}, true
	}
}

func opaqueString(v interface{}) (string, bool) {
	switch v := v.(type) {
	case nil:
		return "", false
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case *big.Int:
		return v.String(), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.FormatInt(int64(v), 10), true
	default:
		js, err := json.Marshal(v)
		return string(js), err == nil
	}
}

func (h *Handler) reply(ctx context.Context, log zerolog.Logger, w http.ResponseWriter, out interface{}) {
	const responseKey = "__response"

	status := http.StatusOK
	mr, _ := out.(map[string]interface{})
	if r, ok := mr[responseKey].(map[string]interface{}); ok && r != nil {
		// HTTP status.
		status64, ok := opaqueInt(r["status"])
		if ok {
			if status64 > math.MaxInt || status64 <= 0 {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				log.Error().Msgf("Cannot cast __response.status (%d) to int without data loss.", status64)
				return
			}
			status = int(status64)
		}

		// HTTP headers.
		headers, _ := r["headers"].(map[string]interface{})
		for k, v := range headers {
			k := http.CanonicalHeaderKey(k)
			hvs, _ := opaqueStrings(v)
			for _, hv := range hvs {
				w.Header().Add(k, hv)
			}
		}

		// Replace output data (in case it needs to be an array and
		// you've embedded it alongside __response.
		dataKey, ok := opaqueString(r["data_key"])
		if ok {
			log.Info().Str("key", dataKey).Msg("Replacing output data")
			out = mr[dataKey]
		}
	}
	delete(mr, responseKey)

	blob, err := json.Marshal(out)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		log.Error().Err(err).Msg("Failed to marshal output.")
		return
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_, err = w.Write(blob)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to write response to client.")
	}
}

func (h *Handler) computeResponse(ctx context.Context, log zerolog.Logger, w http.ResponseWriter, req *http.Request, params *Params, body interface{}) (out interface{}, err error) {
	transactions := make([]*transactionState, len(h.Query.Transactions))
	closeTransactions := func(ctx context.Context, err error) {
		defer log.Trace().Msg("Transactions closed.")
		for ti, t := range transactions {
			if t == nil {
				// Partial setup.
				return
			}
			cerr := t.CommitOrRollback(ctx, err)
			if cerr != nil {
				log.Warn().Int("transaction", ti).Err(cerr).Msg("Error committing or rolling back transaction.")
			}
		}
	}
	defer func() { closeTransactions(ctx, err) }()

	for tdi, td := range h.Query.Transactions {
		db := h.db[td.DB]
		t, err := newTransaction(ctx, db, td)
		if err != nil {
			http.Error(w, "error preparing request", http.StatusInternalServerError)
			log.Error().Err(err).Int("transaction", tdi).Msg("Error starting transaction for request.")
			return nil, err
		}
		transactions[tdi] = t
	}
	log.Trace().Msg("Transactions started.")

	argCtx := argContext{
		body:        body,
		params:      params,
		stepResults: make([]interface{}, 0, len(h.Query.Steps)),
		outputs:     make([]interface{}, 0, len(h.Query.Steps)),
	}
	for si, s := range h.Query.Steps {
		t := transactions[s.Transaction]
		log := log.With().Int("step", si).Logger()

		args := make([]interface{}, len(s.Args))
		for adi, ad := range s.Args {
			arg, err := argCtx.Resolve(ctx, ad)
			if err != nil {
				http.Error(w, "error resolving arguments", http.StatusInternalServerError)
				log.Error().Err(err).Msg("Failed to resolve arguments. This implies an invalid endpoint config.")
				return nil, err
			}
			args[adi] = arg
		}

		argCtx.args = args

		query, args, err := sqlx.In(s.Query, args...)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			log.Error().Err(err).Msg("Failed to expand IN(?) arguments.")
			return nil, err
		}
		query = sqlx.Rebind(t.db.options.BindType, query)

		rows, err := t.QueryContext(ctx, query, args...)
		defer rows.Close()
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			log.Error().Err(err).Msg("Failed to execute query.")
			return nil, err
		}

		results, err := vdb.ScanRows(ctx, rows, t.db.options)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			log.Error().Err(err).Msg("Failed to scan result set.")
			return nil, err
		}

		var res interface{} = results.Opaque()
		log.Info().Interface("args", args).Interface("results", res).Msg("Results.")
		argCtx.stepResults = append(argCtx.stepResults, res)

		res, err = s.Map.Apply(ctx, res, argCtx.Opaque())
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			log.Error().Err(err).Msg("Failed to transform result set.")
			return nil, err
		}

		argCtx.outputs = append(argCtx.outputs, res)
	}

	return argCtx.outputs[len(argCtx.outputs)-1], nil
}

type Committer interface {
	vdb.DB

	Commit() error
	Rollback() error
}

type transactionState struct {
	vdb.DB
	db *Database
}

func (t *transactionState) CommitOrRollback(ctx context.Context, err error) error {
	if err == nil {
		err = ctx.Err()
	}
	c, ok := t.DB.(Committer)
	if !ok {
		return nil
	}

	op, verb := c.Commit, "commit"
	if err != nil {
		op, verb = c.Rollback, "rollback"
	}

	operr := op()
	if operr != nil {
		zerolog.Ctx(ctx).Warn().
			Err(operr).
			Msgf("Error performing %s for transaction.", verb)
	}
	return operr
}

func newTransaction(ctx context.Context, db *Database, td *TransactionDef) (*transactionState, error) {
	if !td.Isolation.RequiresTranscation() {
		return &transactionState{
			DB: db.db,
			db: db,
		}, nil
	}

	tx, err := db.db.BeginTxx(ctx, &sql.TxOptions{
		Isolation: td.Isolation.Level(),
	})
	if err != nil {
		return nil, fmt.Errorf("error beginning transaction: %w", err)
	}
	return &transactionState{DB: tx, db: db}, nil
}

type argContext struct {
	params      *Params
	body        interface{}
	stepResults []interface{}
	outputs     []interface{}
	args        []interface{}
	opaque      map[string]interface{}
}

func (c *argContext) Opaque() map[string]interface{} {
	if c.opaque == nil {
		c.opaque = make(map[string]interface{}, 5)
		c.opaque["params"] = c.params.Opaque()
		c.opaque["body"] = c.body
	}
	// Refresh opaque data that changes.
	c.opaque["args"] = append([]interface{}(nil), c.args...)
	c.opaque["steps"] = append([]interface{}(nil), c.stepResults...)
	c.opaque["outputs"] = append([]interface{}(nil), c.outputs...)
	return c.opaque
}

func (c *argContext) Resolve(ctx context.Context, arg ArgDef) (interface{}, error) {
	switch arg := arg.(type) {
	case ArgLiteral:
		return arg.Literal, nil
	case PathParamRef:
		param, ok := c.params.Path[arg.Name]
		if !ok {
			return nil, fmt.Errorf("path param %q not defined", arg.Name)
		}
		return param, nil
	case QueryParamRef:
		param, ok := c.params.Query[arg.Name]
		if !ok {
			return nil, fmt.Errorf("query param %q not defined", arg.Name)
		}
		return param, nil
	case ExprParam:
		return arg.Expr.Apply(ctx, c.Opaque(), c.Opaque())
	}
	panic(fmt.Errorf("unreachable: bad ArgDef %#+ v", arg))
}
