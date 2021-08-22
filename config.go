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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/go-sockaddr"
	"github.com/itchyny/gojq"
	"go.spiff.io/sql/vdb"
)

type Config struct {
	Bind      []sockaddr.SockAddrMarshaler `json:"bind"`
	Databases map[string]*DatabaseDef      `json:"databases"`
	Modules   map[string]*ModuleDef        `json:"modules"`
	Endpoints EndpointDefs                 `json:"endpoints"`
}

func (c *Config) Validate() error {
	var me *multierror.Error
	// dbsUsed := StringSet{}
	for edi, ed := range c.Endpoints {
		ident := fmt.Sprintf("endpoint=%d method=%q path=%q", edi, ed.Method, ed.Path)
		if err := ed.Validate(); err != nil {
			me = multierror.Append(me, fmt.Errorf("%s failed validation: %w", ident, err))
			continue
		}
	}

	return errorOrNil(me)
}

type DatabaseDef struct {
	URL string `json:"url"`

	MaxIdle     int      `json:"max_idle"`
	MaxIdleTime Duration `json:"max_idle_time"`
	MaxOpen     int      `json:"max_open"`
	MaxLifeTime Duration `json:"max_life_time"`

	Options vdb.QueryOptions `json:"options"`
}

type Duration struct {
	time.Duration
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Duration) UnmarshalText(src []byte) error {
	p, err := time.ParseDuration(string(src))
	if err != nil {
		return err
	}
	*d = Duration{p}
	return nil
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

type EndpointDefs []*EndpointDef

type EndpointDef struct {
	Bind        IntSet                   `json:"bind"`
	Method      string                   `json:"method"`
	Path        string                   `json:"path"`
	QueryParams map[string]*ParamMapping `json:"query_params"`
	PathParams  map[string]*ParamMapping `json:"path_params"`

	Query *QueryDef `json:"query"`
}

func (ed *EndpointDef) Validate() error {
	if ed == nil {
		return errors.New("endpoint definition is nil")
	}
	var me *multierror.Error
	if ed.Method == "" {
		me = multierror.Append(me, errors.New("method is empty"))
	}
	if ed.Path == "" {
		me = multierror.Append(me, errors.New("path is empty"))
	}
	if err := ed.Query.Validate(); err != nil {
		me = multierror.Append(me, fmt.Errorf("query failed validation: %w", err))
	}
	return errorOrNil(me)
}

type QueryDef struct {
	Transactions []*TransactionDef `json:"transactions"`
	Steps        []*StepDef        `json:"steps"`
}

func (qd *QueryDef) Validate() error {
	if qd == nil {
		return errors.New("query definition is nil")
	}
	var me *multierror.Error
	all, refs := IntSet{}, IntSet{}
	for i := range qd.Transactions {
		all.Put(i)
	}
	if len(all) == 0 {
		me = multierror.Append(me, errors.New("no transaction(s) defined"))
	}
	for i, sd := range qd.Steps {
		refs.Put(sd.Transaction)
		if !all.Contains(sd.Transaction) {
			me = multierror.Append(me, fmt.Errorf("step %d refers to undefined transaction %d", i, sd.Transaction))
		}
	}
	if !all.Equal(refs) {
		for i := range refs {
			all.Del(i)
		}
		me = multierror.Append(me, fmt.Errorf("unused transaction(s) in query: %v", all))
	}
	return errorOrNil(me)
}

type StepDef struct {
	Transaction int     `json:"transaction"`
	Query       string  `json:"query"`
	Args        ArgDefs `json:"args"`
}

type TransactionDef struct {
	DB        string         `json:"db"`
	Isolation IsolationLevel `json:"isolation"`
}

type ParamMapping struct {
	Map Mapping `json:"map"`
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
	Options []gojq.CompilerOption
	Query   *gojq.Query
	Code    *gojq.Code
}

func (e *Expr) UnmarshalText(src []byte) error {
	q, err := gojq.Parse(string(src))
	if err != nil {
		return fmt.Errorf("error parsing expression: %w", err)
	}

	c, err := gojq.Compile(q)
	if err != nil {
		return fmt.Errorf("error compiling expression: %w", err)
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

type Mapping []*Expr

var ErrNoMapping = errors.New("no result from output mapping")
var ErrMultipleMapping = errors.New("mapping produced multiple values")

func (m Mapping) Apply(ctx context.Context, input interface{}) (interface{}, error) {
	if len(m) == 0 {
		return input, nil
	}

	var output interface{} = input
	var ok bool
	for i, e := range m {
		iter := e.Code.RunWithContext(ctx, output)
		output, ok = iter.Next()
		if !ok {
			return nil, fmt.Errorf("no value returned by mapping %d: %w", i, ErrNoMapping)
		}
		if err, ok := output.(error); ok {
			return nil, fmt.Errorf("error in mapping %d: %w", i, err)
		}
		_, ok = iter.Next()
		if ok {
			return nil, fmt.Errorf("unexpected results from mapping %d: %w", i, ErrMultipleMapping)
		}
	}

	return output, nil
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

func (is IntSet) Put(i int) {
	is[i] = struct{}{}
}

func (is IntSet) Del(i int) {
	if is != nil {
		delete(is, i)
	}
}

func (is IntSet) Equal(other IntSet) bool {
	if len(is) != len(other) {
		return false
	}
	for i := range is {
		if !other.Contains(i) {
			return false
		}
	}
	return true
}

func (is IntSet) Contains(i int) bool {
	_, ok := is[i]
	return ok
}

func (is IntSet) Ordered() []int {
	ints := make([]int, 0, len(is))
	for i := range is {
		ints = append(ints, i)
	}
	sort.Ints(ints)
	return ints
}

func (is IntSet) String() string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, v := range is.Ordered() {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatInt(int64(v), 10))
	}
	sb.WriteByte('}')
	return sb.String()
}

func (is IntSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(is.Ordered())
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

type StringSet map[string]struct{}

func (ss StringSet) Put(s string) {
	ss[s] = struct{}{}
}

func (ss StringSet) Del(s string) {
	if ss != nil {
		delete(ss, s)
	}
}

func (ss StringSet) Equal(other StringSet) bool {
	if len(ss) != len(other) {
		return false
	}
	for s := range ss {
		if !other.Contains(s) {
			return false
		}
	}
	return true
}

func (ss StringSet) Contains(s string) bool {
	_, ok := ss[s]
	return ok
}

func (ss StringSet) Ordered() []string {
	strs := make([]string, 0, len(ss))
	for s := range ss {
		strs = append(strs, s)
	}
	sort.Strings(strs)
	return strs
}

func (ss StringSet) String() string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, s := range ss.Ordered() {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Quote(s))
	}
	sb.WriteByte('}')
	return sb.String()
}

func (ss StringSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(ss.Ordered())
}

func (ss *StringSet) UnmarshalJSON(src []byte) error {
	var strs []string
	if err := json.Unmarshal(src, &strs); err != nil {
		return err
	}
	m := make(StringSet, len(strs))
	for _, s := range strs {
		m[s] = struct{}{}
	}
	*ss = m
	return nil
}

func errorOrNil(me *multierror.Error) error {
	switch {
	case me == nil || len(me.Errors) == 0:
		return nil
	case len(me.Errors) == 1:
		return me.Errors[0]
	default:
		return me
	}
}
