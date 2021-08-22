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
