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
	"github.com/jmoiron/sqlx"
)

type Databases map[string]*Database

type Database struct {
	db *sqlx.DB

	*DatabaseDef
}

// type Transaction struct {
// 	steps     []*Transaction
// 	isolation IsolationLevel
// }

// type TransactionStep struct {
// 	db    *Database
// 	query string
// 	args  ArgDefs
// }

// func (t *Transaction) Apply(ctx context.Context) (interface{}, error) {
// 	return errors.New("unimplemented")
// }

// func CompileTransaction(dbs map[string]*Database, isolation IsolationLevel, tds []*TransactionDef) (*Transaction, error) {
// 	ts := make([]*Transaction, len)
// 	for it, td := range tds {
// 	}
// }
