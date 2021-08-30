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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/go-sockaddr"
	"github.com/itchyny/gojq"
	"github.com/tailscale/hujson"
	"go.spiff.io/sql/vdb"
	"gopkg.in/yaml.v3"
)

func unmarshalStrict(p []byte, dest interface{}) error {
	dec := hujson.NewDecoder(bytes.NewReader(p))
	dec.DisallowUnknownFields()
	return dec.Decode(&dest)
}

type SockAddr struct {
	sockaddr.SockAddr
}

func (sa SockAddr) MarshalText() ([]byte, error) {
	return []byte(sa.String()), nil
}

func (sa *SockAddr) UnmarshalText(src []byte) error {
	saddr, err := sockaddr.NewSockAddr(string(src))
	if err != nil {
		return fmt.Errorf("error parsing sockaddr: %w", err)
	}
	*sa = SockAddr{saddr}
	return nil
}

type Config struct {
	Bind      []SockAddr              `json:"bind" yaml:"bind"`
	Databases map[string]*DatabaseDef `json:"databases" yaml:"databases"`
	Modules   map[string]*ModuleDef   `json:"modules" yaml:"modules"`
	Endpoints EndpointDefs            `json:"endpoints" yaml:"endpoints"`
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

type QueryOptions struct {
	TryJSON    bool           `json:"try_json" yaml:"try_json"`
	SkipJSON   bool           `json:"skip_json" yaml:"skip_json"`
	TimeFormat vdb.TimeFormat `json:"time_format" yaml:"time_format"`
	TimeLayout string         `json:"time_layout,omitempty" yaml:"time_layout,omitempty"` // Used if TimeFormat is TimeCustom.

	BindType int `json:"-" yaml:"-"`
}

func (q *QueryOptions) QueryOptions() *vdb.QueryOptions {
	if q == nil {
		return &vdb.QueryOptions{}
	}
	return &vdb.QueryOptions{
		TryJSON:    q.TryJSON,
		SkipJSON:   q.SkipJSON,
		TimeFormat: q.TimeFormat,
		TimeLayout: q.TimeLayout,
		Compact:    true,
		BindType:   q.BindType,
	}
}

type DatabaseDef struct {
	URL string `json:"url" yaml:"url"`

	MaxIdle     int      `json:"max_idle" yaml:"max_idle"`
	MaxIdleTime Duration `json:"max_idle_time" yaml:"max_idle_time"`
	MaxOpen     int      `json:"max_open" yaml:"max_open"`
	MaxLifeTime Duration `json:"max_life_time" yaml:"max_life_time"`

	Options QueryOptions      `json:"options" yaml:"options"`
	options *vdb.QueryOptions // Converted options.
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

type BodyType int

const (
	JSONBodyType   BodyType = iota // json - Default
	FormBodyType                   // form
	StringBodyType                 // string
	NoBodyType                     // none
)

func (b BodyType) MarshalText() ([]byte, error) {
	typ := "json"
	switch b {
	case JSONBodyType:
	case FormBodyType:
		typ = "form"
	case StringBodyType:
		typ = "string"
	case NoBodyType:
		typ = "none"
	default:
		return nil, fmt.Errorf("unrecognized body type %d", b)
	}
	return []byte(typ), nil
}

func (b *BodyType) UnmarshalText(src []byte) error {
	switch src := string(src); src {
	case "json":
		*b = JSONBodyType
	case "form":
		*b = FormBodyType
	case "string":
		*b = StringBodyType
	case "none":
		*b = NoBodyType
	default:
		return fmt.Errorf("unrecognized body type %q", src)
	}
	return nil
}

type EndpointDefs []*EndpointDef

type ParamMappings map[string]*ParamMapping

type EndpointDef struct {
	Bind        IntSet        `json:"bind" yaml:"bind"`
	Method      string        `json:"method" yaml:"method"`
	Path        string        `json:"path" yaml:"path"`
	BodyType    BodyType      `json:"body_type" yaml:"body_type"`
	QueryParams ParamMappings `json:"query_params" yaml:"query_params"`
	PathParams  ParamMappings `json:"path_params" yaml:"path_params"`

	Query *QueryDef `json:"query" yaml:"query"`
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
	Transactions []*TransactionDef `json:"transactions" yaml:"transactions"`
	Steps        []*StepDef        `json:"steps" yaml:"steps"`
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
	Transaction int     `json:"transaction" yaml:"transaction"`
	Query       string  `json:"query" yaml:"query"`
	Args        ArgDefs `json:"args" yaml:"args"`
	Map         Mapping `json:"map" yaml:"map"`
}

type TransactionDef struct {
	DB        string         `json:"db" yaml:"db"`
	Isolation IsolationLevel `json:"isolation" yaml:"isolation"`
}

type ParamMapping struct {
	Map Mapping `json:"map" yaml:"map"`
}

type ArgDefs []ArgDef

func (ads *ArgDefs) UnmarshalJSON(src []byte) error {
	var defs []json.RawMessage
	if err := unmarshalStrict(src, &defs); err != nil {
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

func (ads *ArgDefs) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("expected sequence node for arg defs, got %d", node.Kind)
	}
	args := make(ArgDefs, len(node.Content))
	for i, def := range node.Content {
		ad, err := UnmarshalArgDefYAML(def)
		if err != nil {
			return fmt.Errorf("error unmarshaling arg %d: %w", i, err)
		}
		args[i] = ad
	}
	*ads = args
	return nil
}

type ArgDef interface {
	param()
}

var ErrBadArgDef = errors.New("invalid arg def: must be a scalar, null, or contain a single key of 'path', 'query', or 'expr'")

func UnmarshalArgDefYAML(node *yaml.Node) (ArgDef, error) {
	if node.Kind == yaml.SequenceNode {
		return nil, fmt.Errorf("unexpected sequence, expected mapping or other")
	}
	if node.Kind != yaml.MappingNode {
		lit := ArgLiteral{}
		if err := node.Decode(&lit.Literal); err != nil {
			return nil, fmt.Errorf("error unmarshaling arg def as literal: %w", err)
		}
		return lit, nil
	}

	if len(node.Content) != 2 {
		// Mapping has content with two items: a key, and
		// a value.
		return nil, ErrBadArgDef
	}

	var key string
	if err := node.Content[0].Decode(&key); err != nil {
		return nil, fmt.Errorf("error unmarshaling arg def key: %w", err)
	}

	value := node.Content[1]
	switch key {
	case "path":
		var ref PathParamRef
		if err := value.Decode(&ref.Name); err != nil {
			return nil, fmt.Errorf("error unmarshaling path arg def: %w", err)
		}
		return ref, nil
	case "query":
		var ref QueryParamRef
		if err := value.Decode(&ref.Name); err != nil {
			return nil, fmt.Errorf("error unmarshaling query arg def: %w", err)
		}
		return ref, nil
	case "expr":
		var expr Expr
		if err := value.Decode(&expr); err != nil {
			return nil, fmt.Errorf("error unmarshaling expr arg def: %w", err)
		}
		return ExprParam{&expr}, nil
	default:
		return nil, ErrBadArgDef
	}
}

func UnmarshalArgDef(blob json.RawMessage) (ArgDef, error) {
	var m map[string]json.RawMessage
	if unmarshalStrict(blob, &m) == nil {
		if m == nil {
			return ArgLiteral{Literal: nil}, nil
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
			if err := unmarshalStrict(value, &ref.Name); err != nil {
				return nil, fmt.Errorf("error unmarshaling path arg def: %w", err)
			}
			return ref, nil
		case "query":
			var ref QueryParamRef
			if err := unmarshalStrict(value, &ref.Name); err != nil {
				return nil, fmt.Errorf("error unmarshaling query arg def: %w", err)
			}
			return ref, nil
		case "expr":
			var expr Expr
			if err := unmarshalStrict(value, &expr); err != nil {
				return nil, fmt.Errorf("error unmarshaling expr arg def: %w", err)
			}
			return ExprParam{&expr}, nil
		default:
			return nil, ErrBadArgDef
		}
	}

	lit := ArgLiteral{}
	if err := unmarshalStrict(blob, &lit.Literal); err != nil {
		return nil, fmt.Errorf("error unmarshaling arg def as literal: %w", err)
	}

	return lit, nil
}

type ArgLiteral struct {
	Literal interface{}
}

func (a ArgLiteral) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.Literal)
}

func (ArgLiteral) param() {}

type PathParamRef struct {
	Name string `json:"path" yaml:"path"`
}

func (p PathParamRef) Value() (interface{}, error) {
	return nil, errors.New("unimplemented")
}

func (PathParamRef) param() {}

type QueryParamRef struct {
	Name string `json:"query" yaml:"query"`
}

func (QueryParamRef) param() {}

type ExprParam struct {
	Expr *Expr `json:"expr" yaml:"expr"`
}

func (ExprParam) param() {}

type Expr struct {
	Options []gojq.CompilerOption
	Query   *gojq.Query
	Code    *gojq.Code
}

func gojqDebug(input interface{}, args []interface{}) interface{} {
	data, err := json.Marshal(input)
	if err != nil {
		return input
	}
	fmt.Fprintf(os.Stderr, "DEBUG: %s\n", data)
	return input
}

func (e *Expr) UnmarshalText(src []byte) error {
	q, err := gojq.Parse(string(src))
	if err != nil {
		return fmt.Errorf("error parsing expression: %w", err)
	}

	c, err := gojq.Compile(q, gojq.WithVariables([]string{"$context"}))
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

func (e *Expr) Apply(ctx context.Context, input, ctxVar interface{}) (interface{}, error) {
	iter := e.Code.RunWithContext(ctx, input, ctxVar)
	output, ok := iter.Next()
	if !ok {
		return nil, fmt.Errorf("no value returned by mapping: %w", ErrNoMapping)
	}
	if err, ok := output.(error); ok {
		return nil, fmt.Errorf("error returned by mapping: %w", err)
	}
	_, ok = iter.Next()
	if ok {
		return nil, fmt.Errorf("unexpected results from mapping: %w", ErrMultipleMapping)
	}
	return output, nil
}

type Mapping []*Expr

var ErrNoMapping = errors.New("no result from output mapping")
var ErrMultipleMapping = errors.New("mapping produced multiple values")

func (m Mapping) Apply(ctx context.Context, input, ctxVar interface{}) (interface{}, error) {
	if len(m) == 0 {
		return input, nil
	}

	var output interface{} = input
	var err error
	for i, e := range m {
		output, err = e.Apply(ctx, output, ctxVar)
		if err != nil {
			return nil, fmt.Errorf("error applying mapping %d: %w", i, err)
		}
	}

	return output, err
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

func (is IntSet) MarshalYAML() (interface{}, error) {
	return is.Ordered(), nil
}

func (is *IntSet) UnmarshalJSON(src []byte) error {
	var ints []int
	if err := unmarshalStrict(src, &ints); err != nil {
		return err
	}
	m := make(IntSet, len(ints))
	for _, i := range ints {
		m[i] = struct{}{}
	}
	*is = m
	return nil
}

func (is *IntSet) UnmarshalYAML(node *yaml.Node) error {
	var ints []int
	if err := node.Decode(&ints); err != nil {
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

func (ss StringSet) MarshalYAML() (interface{}, error) {
	return ss.Ordered(), nil
}

func (ss *StringSet) UnmarshalJSON(src []byte) error {
	var strs []string
	if err := unmarshalStrict(src, &strs); err != nil {
		return err
	}
	m := make(StringSet, len(strs))
	for _, s := range strs {
		m[s] = struct{}{}
	}
	*ss = m
	return nil
}

func (ss *StringSet) UnmarshalYAML(node *yaml.Node) error {
	var strs []string
	if err := node.Decode(&strs); err != nil {
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
