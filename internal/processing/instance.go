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

package processing

import (
	"context"
	"fmt"
	"sort"

	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/config"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/log"
	"github.com/superseriousbusiness/gotosocial/internal/text"
	"github.com/superseriousbusiness/gotosocial/internal/util"
	"github.com/superseriousbusiness/gotosocial/internal/validate"
)

func (p *Processor) getThisInstance(ctx context.Context) (*gtsmodel.Instance, error) {
	instance, err := p.state.DB.GetInstance(ctx, config.GetHost())
	if err != nil {
		return nil, err
	}

	return instance, nil
}

func (p *Processor) InstanceGetV1(ctx context.Context) (*apimodel.InstanceV1, gtserror.WithCode) {
	i, err := p.getThisInstance(ctx)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error fetching instance: %s", err))
	}

	ai, err := p.tc.InstanceToAPIV1Instance(ctx, i)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error converting instance to api representation: %s", err))
	}

	return ai, nil
}

func (p *Processor) InstanceGetV2(ctx context.Context) (*apimodel.InstanceV2, gtserror.WithCode) {
	i, err := p.getThisInstance(ctx)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error fetching instance: %s", err))
	}

	ai, err := p.tc.InstanceToAPIV2Instance(ctx, i)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error converting instance to api representation: %s", err))
	}

	return ai, nil
}

func (p *Processor) InstancePeersGet(ctx context.Context, includeSuspended bool, includeOpen bool, flat bool) (interface{}, gtserror.WithCode) {
	domains := []*apimodel.Domain{}

	if includeOpen {
		instances, err := p.state.DB.GetInstancePeers(ctx, false)
		if err != nil && err != db.ErrNoEntries {
			err = fmt.Errorf("error selecting instance peers: %s", err)
			return nil, gtserror.NewErrorInternalError(err)
		}

		for _, i := range instances {
			// Domain may be in Punycode,
			// de-punify it just in case.
			d, err := util.DePunify(i.Domain)
			if err != nil {
				log.Errorf(ctx, "couldn't depunify domain %s: %s", i.Domain, err)
				continue
			}

			domains = append(domains, &apimodel.Domain{Domain: d})
		}
	}

	if includeSuspended {
		domainBlocks := []*gtsmodel.DomainBlock{}
		if err := p.state.DB.GetAll(ctx, &domainBlocks); err != nil && err != db.ErrNoEntries {
			return nil, gtserror.NewErrorInternalError(err)
		}

		for _, domainBlock := range domainBlocks {
			// Domain may be in Punycode,
			// de-punify it just in case.
			d, err := util.DePunify(domainBlock.Domain)
			if err != nil {
				log.Errorf(ctx, "couldn't depunify domain %s: %s", domainBlock.Domain, err)
				continue
			}

			if *domainBlock.Obfuscate {
				// Obfuscate the de-punified version.
				d = obfuscate(d)
			}

			domains = append(domains, &apimodel.Domain{
				Domain:        d,
				SuspendedAt:   util.FormatISO8601(domainBlock.CreatedAt),
				PublicComment: domainBlock.PublicComment,
			})
		}
	}

	sort.Slice(domains, func(i, j int) bool {
		return domains[i].Domain < domains[j].Domain
	})

	if flat {
		flattened := []string{}
		for _, d := range domains {
			flattened = append(flattened, d.Domain)
		}
		return flattened, nil
	}

	return domains, nil
}

func (p *Processor) InstancePatch(ctx context.Context, form *apimodel.InstanceSettingsUpdateRequest) (*apimodel.InstanceV1, gtserror.WithCode) {
	// fetch the instance entry from the db for processing
	host := config.GetHost()

	instance, err := p.state.DB.GetInstance(ctx, host)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error fetching instance %s: %s", host, err))
	}

	// fetch the instance account from the db for processing
	ia, err := p.state.DB.GetInstanceAccount(ctx, "")
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error fetching instance account %s: %s", host, err))
	}

	updatingColumns := []string{}

	// validate & update site title if it's set on the form
	if form.Title != nil {
		if err := validate.SiteTitle(*form.Title); err != nil {
			return nil, gtserror.NewErrorBadRequest(err, fmt.Sprintf("site title invalid: %s", err))
		}
		updatingColumns = append(updatingColumns, "title")
		instance.Title = text.SanitizePlaintext(*form.Title) // don't allow html in site title
	}

	// validate & update site contact account if it's set on the form
	if form.ContactUsername != nil {
		// make sure the account with the given username exists in the db
		contactAccount, err := p.state.DB.GetAccountByUsernameDomain(ctx, *form.ContactUsername, "")
		if err != nil {
			return nil, gtserror.NewErrorBadRequest(err, fmt.Sprintf("account with username %s not retrievable", *form.ContactUsername))
		}
		// make sure it has a user associated with it
		contactUser, err := p.state.DB.GetUserByAccountID(ctx, contactAccount.ID)
		if err != nil {
			return nil, gtserror.NewErrorBadRequest(err, fmt.Sprintf("user for account with username %s not retrievable", *form.ContactUsername))
		}
		// suspended accounts cannot be contact accounts
		if !contactAccount.SuspendedAt.IsZero() {
			err := fmt.Errorf("selected contact account %s is suspended", contactAccount.Username)
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		// unconfirmed or unapproved users cannot be contacts
		if contactUser.ConfirmedAt.IsZero() {
			err := fmt.Errorf("user of selected contact account %s is not confirmed", contactAccount.Username)
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		if !*contactUser.Approved {
			err := fmt.Errorf("user of selected contact account %s is not approved", contactAccount.Username)
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		// contact account user must be admin or moderator otherwise what's the point of contacting them
		if !*contactUser.Admin && !*contactUser.Moderator {
			err := fmt.Errorf("user of selected contact account %s is neither admin nor moderator", contactAccount.Username)
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		updatingColumns = append(updatingColumns, "contact_account_id")
		instance.ContactAccountID = contactAccount.ID
	}

	// validate & update site contact email if it's set on the form
	if form.ContactEmail != nil {
		contactEmail := *form.ContactEmail
		if contactEmail != "" {
			if err := validate.Email(contactEmail); err != nil {
				return nil, gtserror.NewErrorBadRequest(err, err.Error())
			}
		}
		updatingColumns = append(updatingColumns, "contact_email")
		instance.ContactEmail = contactEmail
	}

	// validate & update site short description if it's set on the form
	if form.ShortDescription != nil {
		if err := validate.SiteShortDescription(*form.ShortDescription); err != nil {
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		updatingColumns = append(updatingColumns, "short_description")
		instance.ShortDescription = text.SanitizeHTML(*form.ShortDescription) // html is OK in site description, but we should sanitize it
	}

	// validate & update site description if it's set on the form
	if form.Description != nil {
		if err := validate.SiteDescription(*form.Description); err != nil {
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		updatingColumns = append(updatingColumns, "description")
		instance.Description = text.SanitizeHTML(*form.Description) // html is OK in site description, but we should sanitize it
	}

	// validate & update site terms if it's set on the form
	if form.Terms != nil {
		if err := validate.SiteTerms(*form.Terms); err != nil {
			return nil, gtserror.NewErrorBadRequest(err, err.Error())
		}
		updatingColumns = append(updatingColumns, "terms")
		instance.Terms = text.SanitizeHTML(*form.Terms) // html is OK in site terms, but we should sanitize it
	}

	var updateInstanceAccount bool

	if form.Avatar != nil && form.Avatar.Size != 0 {
		// process instance avatar image + description
		avatarInfo, err := p.account.UpdateAvatar(ctx, form.Avatar, form.AvatarDescription, ia.ID)
		if err != nil {
			return nil, gtserror.NewErrorBadRequest(err, "error processing avatar")
		}
		ia.AvatarMediaAttachmentID = avatarInfo.ID
		ia.AvatarMediaAttachment = avatarInfo
		updateInstanceAccount = true
	} else if form.AvatarDescription != nil && ia.AvatarMediaAttachment != nil {
		// process just the description for the existing avatar
		ia.AvatarMediaAttachment.Description = *form.AvatarDescription
		if err := p.state.DB.UpdateAttachment(ctx, ia.AvatarMediaAttachment, "description"); err != nil {
			return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error updating instance avatar description: %s", err))
		}
	}

	if form.Header != nil && form.Header.Size != 0 {
		// process instance header image
		headerInfo, err := p.account.UpdateHeader(ctx, form.Header, nil, ia.ID)
		if err != nil {
			return nil, gtserror.NewErrorBadRequest(err, "error processing header")
		}
		ia.HeaderMediaAttachmentID = headerInfo.ID
		ia.HeaderMediaAttachment = headerInfo
		updateInstanceAccount = true
	}

	if updateInstanceAccount {
		// if either avatar or header is updated, we need
		// to update the instance account that stores them
		if err := p.state.DB.UpdateAccount(ctx, ia); err != nil {
			return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error updating instance account: %s", err))
		}
	}

	if len(updatingColumns) != 0 {
		if err := p.state.DB.UpdateInstance(ctx, instance, updatingColumns...); err != nil {
			return nil, gtserror.NewErrorInternalError(fmt.Errorf("db error updating instance %s: %s", host, err))
		}
	}

	ai, err := p.tc.InstanceToAPIV1Instance(ctx, instance)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error converting instance to api representation: %s", err))
	}

	return ai, nil
}

func obfuscate(domain string) string {
	obfuscated := make([]rune, len(domain))
	for i, r := range domain {
		if i%3 == 1 || i%5 == 1 {
			obfuscated[i] = '*'
		} else {
			obfuscated[i] = r
		}
	}
	return string(obfuscated)
}
