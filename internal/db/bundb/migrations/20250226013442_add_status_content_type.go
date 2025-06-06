// GoToSocial
// Copyright (C) GoToSocial Authors admin@gotosocial.org
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package migrations

import (
	"context"
	"reflect"

	gtsmodel "code.superseriousbusiness.org/gotosocial/internal/db/bundb/migrations/20250226013442_add_status_content_type"

	"github.com/uptrace/bun"
)

func init() {
	up := func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {

			// Generate new Status.ContentType column definition from bun.
			statusType := reflect.TypeOf((*gtsmodel.Status)(nil))
			colDef, err := getBunColumnDef(tx, statusType, "ContentType")
			if err != nil {
				return err
			}

			// Add column to Status table.
			_, err = tx.NewAddColumn().
				Model((*gtsmodel.Status)(nil)).
				ColumnExpr(colDef).
				Exec(ctx)
			if err != nil {
				return err
			}

			// same for StatusEdit

			statusEditType := reflect.TypeOf((*gtsmodel.StatusEdit)(nil))
			colDef, err = getBunColumnDef(tx, statusEditType, "ContentType")
			if err != nil {
				return err
			}

			_, err = tx.NewAddColumn().
				Model((*gtsmodel.StatusEdit)(nil)).
				ColumnExpr(colDef).
				Exec(ctx)
			if err != nil {
				return err
			}

			return nil
		})
	}

	down := func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return nil
		})
	}

	if err := Migrations.Register(up, down); err != nil {
		panic(err)
	}
}
