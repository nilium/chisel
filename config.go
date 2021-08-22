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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-sockaddr"
	"github.com/itchyny/gojq"
	"go.spiff.io/sql/vdb"
)

type Config struct {
	Bind      []sockaddr.SockAddrMarshaler `json:"bind"`
	Databases map[string]*DatabaseDef      `json:"databases"`
	Modules   map[string]*ModuleDef        `json:"modules"`
	Endpoints []*EndpointDef               `json:"endpoints"`
}

type DatabaseDef struct {
	URL     string           `json:"url"`
	Options vdb.QueryOptions `json:"options"`
}

type ModuleDef struct {
}

type IsolationLevel sql.IsolationLevel

func (i *IsolationLevel) UnmarshalText(src []byte) error {
	switch s := string(src); s {
	case "none":
		*i = -1
	case "default":
		*i = IsolationLevel(sql.LevelDefault)
	case "read_uncommitted":
		*i = IsolationLevel(sql.LevelReadUncommitted)
	case "read_committed":
		*i = IsolationLevel(sql.LevelReadCommitted)
	case "write_committed":
		*i = IsolationLevel(sql.LevelWriteCommitted)
	case "repeatable_read":
		*i = IsolationLevel(sql.LevelRepeatableRead)
	case "snapshot":
		*i = IsolationLevel(sql.LevelSnapshot)
	case "serializable":
		*i = IsolationLevel(sql.LevelSerializable)
	case "linearizable":
		*i = IsolationLevel(sql.LevelLinearizable)
	default:
		return fmt.Errorf("unrecognized isolation level %q", s)
	}
	return nil
}

func (i IsolationLevel) MarshalText() ([]byte, error) {
	switch i {
	case -1:
		return []byte("none"), nil
	case IsolationLevel(sql.LevelDefault):
		return []byte("default"), nil
	case IsolationLevel(sql.LevelReadUncommitted):
		return []byte("read_uncommitted"), nil
	case IsolationLevel(sql.LevelReadCommitted):
		return []byte("read_committed"), nil
	case IsolationLevel(sql.LevelWriteCommitted):
		return []byte("write_committed"), nil
	case IsolationLevel(sql.LevelRepeatableRead):
		return []byte("repeatable_read"), nil
	case IsolationLevel(sql.LevelSnapshot):
		return []byte("snapshot"), nil
	case IsolationLevel(sql.LevelSerializable):
		return []byte("serializable"), nil
	case IsolationLevel(sql.LevelLinearizable):
		return []byte("linearizable"), nil
	default:
		return nil, fmt.Errorf("unrecognized isolation level %d", i)
	}
}

func (i IsolationLevel) RequiresTranscation() bool {
	return i != -1
}

func (i IsolationLevel) Level() sql.IsolationLevel {
	return sql.IsolationLevel(i)
}

type EndpointDef struct {
	Bind        IntSet                   `json:"bind"`
	Method      string                   `json:"method"`
	Path        string                   `json:"path"`
	QueryParams map[string]*ParamMapping `json:"query_params"`
	PathParams  map[string]*ParamMapping `json:"path_params"`

	TransactionIsolation IsolationLevel    `json:"isolation"`
	Transaction          []*TransactionDef `json:"transaction"`
}

type ParamMapping struct {
	Map Mapping `json:"map"`
}

type TransactionDef struct {
	Query string  `json:"query"`
	Args  ArgDefs `json:"args"`
}

type ArgDefs []ArgDef

func (ads *ArgDefs) UnmarshalJSON(src []byte) error {
	var defs []json.RawMessage
	if err := json.Unmarshal(src, &defs); err != nil {
		return err
	}

	args := make(ArgDefs, len(defs))
	for i, def := range defs {
		ad, err := UnmarshalArgDef(def)
		if err != nil {
			return fmt.Errorf("error unmarshaling arg %d: %w", i, err)
		}
		args[i] = ad
	}
	*ads = args
	return nil
}

type ArgDef interface {
	Value() (interface{}, error)
}

var ErrBadArgDef = errors.New("invalid arg def: must be a scalar, null, or contain a single key of 'path' or 'query'")

func UnmarshalArgDef(blob json.RawMessage) (ArgDef, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(blob, &m) == nil {
		if m == nil {
			return &ArgLiteral{Literal: nil}, nil
		}
		if len(m) != 1 {
			return nil, ErrBadArgDef
		}
		var key string
		var value json.RawMessage
		for k, v := range m {
			key = k
			value = v
		}
		switch key {
		case "path":
			var ref PathParamRef
			if err := json.Unmarshal(value, &ref.Name); err != nil {
				return nil, fmt.Errorf("error unmarshaling path arg def: %w", err)
			}
			return &ref, nil
		case "query":
			var ref QueryParamRef
			if err := json.Unmarshal(value, &ref.Name); err != nil {
				return nil, fmt.Errorf("error unmarshaling query arg def: %w", err)
			}
			return &ref, nil
		default:
			return nil, ErrBadArgDef
		}
	}

	lit := &ArgLiteral{}
	if err := json.Unmarshal(blob, &lit.Literal); err != nil {
		return nil, fmt.Errorf("error unmarshaling arg def as literal: %w", err)
	}

	if _, ok := lit.Literal.([]interface{}); ok {
		return nil, ErrBadArgDef
	}

	return lit, nil
}

type ArgLiteral struct {
	Literal interface{}
}

func (a ArgLiteral) Value() (interface{}, error) {
	return a.Literal, nil
}

func (a ArgLiteral) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.Literal)
}

type PathParamRef struct {
	Name string `json:"path"`
}

func (p PathParamRef) Value() (interface{}, error) {
	return nil, errors.New("unimplemented")
}

type QueryParamRef struct {
	Name string `json:"query"`
}

func (q QueryParamRef) Value() (interface{}, error) {
	return nil, errors.New("unimplemented")
}

type Expr struct {
	ID      string
	Options []gojq.CompilerOption
	Query   *gojq.Query
	Code    *gojq.Code
}

func (e *Expr) UnmarshalText(src []byte) error {
	q, err := gojq.Parse(string(src))
	if err != nil {
		return fmt.Errorf("error parsing expression %s: %w", e.ID, err)
	}

	c, err := gojq.Compile(q)
	if err != nil {
		return fmt.Errorf("error compiling expression %s: %w", e.ID, err)
	}

	dup := *e
	dup.Query = q
	dup.Code = c
	*e = dup
	return nil
}

func (e *Expr) MarshalText() ([]byte, error) {
	return []byte(e.Query.String()), nil
}

type Mapping struct {
	ID    string
	Exprs []*Expr
}

var ErrNoMapping = errors.New("no result from output mapping")
var ErrMultipleMapping = errors.New("mapping produced multiple values")

func (m *Mapping) Apply(ctx context.Context, str string) (interface{}, error) {
	if len(m.Exprs) == 0 {
		return str, nil
	}

	var v interface{} = str
	var ok bool
	for _, e := range m.Exprs {
		iter := e.Code.RunWithContext(ctx, v)
		v, ok = iter.Next()
		if !ok {
			return nil, fmt.Errorf("no value returned by mapping %s: %w", e.ID, ErrNoMapping)
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("error in mapping %s: %w", e.ID, err)
		}
		_, ok = iter.Next()
		if ok {
			return nil, fmt.Errorf("unexpected results from mapping %s: %w", e.ID, ErrMultipleMapping)
		}
	}

	return v, nil
}

func (m *Mapping) UnmarshalJSON(src []byte) error {
	var strs []string
	if err := json.Unmarshal(src, &strs); err != nil {
		return fmt.Errorf("error unmarshaling expression list %s: %w", m.ID, err)
	}

	exprs := make([]*Expr, len(strs))
	for i, es := range strs {
		e := &Expr{
			ID: indexID(m.ID, "map", i),
		}
		if err := e.UnmarshalText([]byte(es)); err != nil {
			return fmt.Errorf("error unmarshaling mapping expression %s: %w", e.ID, err)
		}
		exprs[i] = e
	}

	dup := *m
	dup.Exprs = exprs
	*m = dup

	return nil
}

func (m *Mapping) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.Exprs)
}

func nameID(prefix, name string) string {
	var sb strings.Builder
	if prefix != "" {
		_, _ = sb.WriteString(prefix)
		_, _ = sb.WriteString("/")
	}
	_, _ = sb.WriteString(name)
	return sb.String()
}

func indexID(prefix, name string, index int) string {
	var sb strings.Builder
	if prefix != "" {
		_, _ = sb.WriteString(prefix)
		_ = sb.WriteByte('.')
	}
	_, _ = sb.WriteString(name)
	_ = sb.WriteByte('[')
	_, _ = sb.WriteString(strconv.Itoa(index))
	_ = sb.WriteByte(']')
	return sb.String()
}

type IntSet map[int]struct{}

func (is IntSet) Contains(i int) bool {
	_, ok := is[i]
	return ok
}

func (is IntSet) MarshalJSON() ([]byte, error) {
	ints := make([]int, len(is))
	for i := range is {
		ints = append(ints, i)
	}
	sort.Ints(ints)
	return json.Marshal(ints)
}

func (is *IntSet) UnmarshalJSON(src []byte) error {
	var ints []int
	if err := json.Unmarshal(src, &ints); err != nil {
		return err
	}
	m := make(IntSet, len(ints))
	for _, i := range ints {
		m[i] = struct{}{}
	}
	*is = m
	return nil
}
