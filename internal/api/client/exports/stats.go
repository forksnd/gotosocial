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

package exports

import (
	"net/http"

	apiutil "code.superseriousbusiness.org/gotosocial/internal/api/util"
	"code.superseriousbusiness.org/gotosocial/internal/gtserror"
	"github.com/gin-gonic/gin"
)

// ExportStatsGETHandler swagger:operation GET /api/v1/exports/stats exportStats
//
// Returns informational stats on the number of items that can be exported for requesting account.
//
//	---
//	tags:
//	- import-export
//
//	produces:
//	- application/json
//
//	security:
//	- OAuth2 Bearer:
//		- read:accounts
//
//	responses:
//		'200':
//			description: Export stats for the requesting account.
//			schema:
//				"$ref": "#/definitions/accountExportStats"
//		'401':
//			description: unauthorized
//		'406':
//			description: not acceptable
//		'500':
//			description: internal server error
func (m *Module) ExportStatsGETHandler(c *gin.Context) {
	authed, errWithCode := apiutil.TokenAuth(c,
		true, true, true, true,
		apiutil.ScopeReadAccounts,
	)
	if errWithCode != nil {
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	if _, err := apiutil.NegotiateAccept(c, apiutil.JSONAcceptHeaders...); err != nil {
		apiutil.ErrorHandler(c, gtserror.NewErrorNotAcceptable(err, err.Error()), m.processor.InstanceGetV1)
		return
	}

	exportStats, errWithCode := m.processor.Account().ExportStats(
		c.Request.Context(),
		authed.Account,
	)
	if errWithCode != nil {
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	apiutil.JSON(c, http.StatusOK, exportStats)
}
