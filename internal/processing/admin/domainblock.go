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

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"time"

	"codeberg.org/gruf/go-kv"
	"github.com/superseriousbusiness/gotosocial/internal/ap"
	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/id"
	"github.com/superseriousbusiness/gotosocial/internal/log"
	"github.com/superseriousbusiness/gotosocial/internal/messages"
	"github.com/superseriousbusiness/gotosocial/internal/text"
	"github.com/superseriousbusiness/gotosocial/internal/util"
)

func (p *Processor) DomainBlockCreate(
	ctx context.Context,
	account *gtsmodel.Account,
	domain string,
	obfuscate bool,
	publicComment string,
	privateComment string,
	subscriptionID string,
) (*apimodel.DomainBlock, gtserror.WithCode) {
	// Normalize the domain as punycode
	var err error
	domain, err = util.Punify(domain)
	if err != nil {
		err := gtserror.Newf("error punifying domain %s: %w", domain, err)
		return nil, gtserror.NewErrorInternalError(err)
	}

	// Check if a block already exists for this domain.
	domainBlock, err := p.state.DB.GetDomainBlock(ctx, domain)
	if err != nil && !errors.Is(err, db.ErrNoEntries) {
		// Something went wrong in the DB.
		err = gtserror.Newf("db error getting domain block %s: %w", domain, err)
		return nil, gtserror.NewErrorInternalError(err)
	}

	if domainBlock != nil {
		// Nothing to do.
		return p.apiDomainBlock(ctx, domainBlock)
	}

	// No block exists yet, create it.
	domainBlock = &gtsmodel.DomainBlock{
		ID:                 id.NewULID(),
		Domain:             domain,
		CreatedByAccountID: account.ID,
		PrivateComment:     text.SanitizePlaintext(privateComment),
		PublicComment:      text.SanitizePlaintext(publicComment),
		Obfuscate:          &obfuscate,
		SubscriptionID:     subscriptionID,
	}

	// Insert the new block into the database.
	if err := p.state.DB.CreateDomainBlock(ctx, domainBlock); err != nil {
		err = gtserror.Newf("db error putting domain block %s: %s", domain, err)
		return nil, gtserror.NewErrorInternalError(err)
	}

	// Process the side effects of the domain block
	// asynchronously since it might take a while.
	go p.domainBlockSideEffects(
		// Use new context, not request context.
		context.Background(),
		account,
		domainBlock,
	)

	return p.apiDomainBlock(ctx, domainBlock)
}

// domainBlockSideEffects should be called asynchronously, to process the side effects of a domain block:
//
// 1. Strip most info away from the instance entry for the domain.
// 2. Delete the instance account for that instance if it exists.
// 3. Select all accounts from this instance and pass them through the delete functionality of the processor.
func (p *Processor) domainBlockSideEffects(ctx context.Context, account *gtsmodel.Account, block *gtsmodel.DomainBlock) {
	l := log.
		WithContext(ctx).
		WithFields(kv.Fields{
			{"domain", block.Domain},
		}...)
	l.Debug("processing domain block side effects")

	// If we have an instance entry for this domain,
	// update it with the new block ID and clear all fields
	instance, err := p.state.DB.GetInstance(ctx, block.Domain)
	if err != nil && !errors.Is(err, db.ErrNoEntries) {
		l.Errorf("db error getting instance %s: %q", block.Domain, err)
	}

	if instance != nil {
		// We had an entry for this domain.
		columns := stubbifyInstance(instance, block.ID)
		if err := p.state.DB.UpdateInstance(ctx, instance, columns...); err != nil {
			l.Errorf("db error updating instance: %s", err)
		} else {
			l.Debug("instance entry updated")
		}
	}

	// if we have an instance account for this instance, delete it
	if instanceAccount, err := p.state.DB.GetAccountByUsernameDomain(ctx, block.Domain, block.Domain); err == nil {
		if err := p.state.DB.DeleteAccount(ctx, instanceAccount.ID); err != nil {
			l.Errorf("domainBlockProcessSideEffects: db error deleting instance account: %s", err)
		}
	}

	// delete accounts through the normal account deletion system (which should also delete media + posts + remove posts from timelines)

	limit := 20      // just select 20 accounts at a time so we don't nuke our DB/mem with one huge query
	var maxID string // this is initially an empty string so we'll start at the top of accounts list (sorted by ID)

selectAccountsLoop:
	for {
		accounts, err := p.state.DB.GetInstanceAccounts(ctx, block.Domain, maxID, limit)
		if err != nil {
			if err == db.ErrNoEntries {
				// no accounts left for this instance so we're done
				l.Infof("domainBlockProcessSideEffects: done iterating through accounts for domain %s", block.Domain)
				break selectAccountsLoop
			}
			// an actual error has occurred
			l.Errorf("domainBlockProcessSideEffects: db error selecting accounts for domain %s: %s", block.Domain, err)
			break selectAccountsLoop
		}

		for i, a := range accounts {
			l.Debugf("putting delete for account %s in the clientAPI channel", a.Username)

			// pass the account delete through the client api channel for processing
			p.state.Workers.EnqueueClientAPI(ctx, messages.FromClientAPI{
				APObjectType:   ap.ActorPerson,
				APActivityType: ap.ActivityDelete,
				GTSModel:       block,
				OriginAccount:  account,
				TargetAccount:  a,
			})

			// if this is the last account in the slice, set the maxID appropriately for the next query
			if i == len(accounts)-1 {
				maxID = a.ID
			}
		}
	}
}

// DomainBlocksImport handles the import of a bunch of domain blocks at once, by calling the DomainBlockCreate function for each domain in the provided file.
func (p *Processor) DomainBlocksImport(ctx context.Context, account *gtsmodel.Account, domains *multipart.FileHeader) ([]*apimodel.DomainBlock, gtserror.WithCode) {
	f, err := domains.Open()
	if err != nil {
		return nil, gtserror.NewErrorBadRequest(fmt.Errorf("DomainBlocksImport: error opening attachment: %s", err))
	}
	buf := new(bytes.Buffer)
	size, err := io.Copy(buf, f)
	if err != nil {
		return nil, gtserror.NewErrorBadRequest(fmt.Errorf("DomainBlocksImport: error reading attachment: %s", err))
	}
	if size == 0 {
		return nil, gtserror.NewErrorBadRequest(errors.New("DomainBlocksImport: could not read provided attachment: size 0 bytes"))
	}

	d := []apimodel.DomainBlock{}
	if err := json.Unmarshal(buf.Bytes(), &d); err != nil {
		return nil, gtserror.NewErrorBadRequest(fmt.Errorf("DomainBlocksImport: could not read provided attachment: %s", err))
	}

	blocks := []*apimodel.DomainBlock{}
	for _, d := range d {
		block, err := p.DomainBlockCreate(ctx, account, d.Domain.Domain, false, d.PublicComment, "", "")
		if err != nil {
			return nil, err
		}

		blocks = append(blocks, block)
	}

	return blocks, nil
}

// DomainBlocksGet returns all existing domain blocks.
// If export is true, the format will be suitable for writing out to an export.
func (p *Processor) DomainBlocksGet(ctx context.Context, account *gtsmodel.Account, export bool) ([]*apimodel.DomainBlock, gtserror.WithCode) {
	domainBlocks := []*gtsmodel.DomainBlock{}

	if err := p.state.DB.GetAll(ctx, &domainBlocks); err != nil {
		if !errors.Is(err, db.ErrNoEntries) {
			// something has gone really wrong
			return nil, gtserror.NewErrorInternalError(err)
		}
	}

	apiDomainBlocks := []*apimodel.DomainBlock{}
	for _, b := range domainBlocks {
		apiDomainBlock, err := p.tc.DomainBlockToAPIDomainBlock(ctx, b, export)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
		apiDomainBlocks = append(apiDomainBlocks, apiDomainBlock)
	}

	return apiDomainBlocks, nil
}

// DomainBlockGet returns one domain block with the given id.
// If export is true, the format will be suitable for writing out to an export.
func (p *Processor) DomainBlockGet(ctx context.Context, account *gtsmodel.Account, id string, export bool) (*apimodel.DomainBlock, gtserror.WithCode) {
	domainBlock := &gtsmodel.DomainBlock{}

	if err := p.state.DB.GetByID(ctx, id, domainBlock); err != nil {
		if !errors.Is(err, db.ErrNoEntries) {
			// something has gone really wrong
			return nil, gtserror.NewErrorInternalError(err)
		}
		// there are no entries for this ID
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("no entry for ID %s", id))
	}

	apiDomainBlock, err := p.tc.DomainBlockToAPIDomainBlock(ctx, domainBlock, export)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	return apiDomainBlock, nil
}

// DomainBlockDelete removes one domain block with the given ID.
func (p *Processor) DomainBlockDelete(ctx context.Context, account *gtsmodel.Account, id string) (*apimodel.DomainBlock, gtserror.WithCode) {
	domainBlock, err := p.state.DB.GetDomainBlockByID(ctx, id)
	if err != nil {
		if !errors.Is(err, db.ErrNoEntries) {
			// Real error.
			err = gtserror.Newf("db error getting domain block: %w", err)
			return nil, gtserror.NewErrorInternalError(err)
		}

		// There are just no entries for this ID.
		err = fmt.Errorf("no domain block entry exists with ID %s", id)
		return nil, gtserror.NewErrorNotFound(err, err.Error())
	}

	// Prepare the domain block to return, *before* the deletion goes through.
	apiDomainBlock, err := p.tc.DomainBlockToAPIDomainBlock(ctx, domainBlock, false)
	if err != nil {
		err = gtserror.Newf("error converting domain block to api domain block: %w", err)
		return nil, gtserror.NewErrorInternalError(err)
	}

	// Delete the domain block.
	if err := p.state.DB.DeleteDomainBlock(ctx, domainBlock.Domain); err != nil {
		err = gtserror.Newf("db error deleting domain block: %w", err)
		return nil, gtserror.NewErrorInternalError(err)
	}

	// Update instance entry for this domain, if we have it.
	instance, err := p.state.DB.GetInstance(ctx, domainBlock.Domain)
	if err != nil && !errors.Is(err, db.ErrNoEntries) {
		err = gtserror.Newf("db error getting instance with domain %s: %w", domainBlock.Domain, err)
		return nil, gtserror.NewErrorInternalError(err)
	}

	if instance != nil {
		// We had an entry, update it to signal
		// that it's no longer suspended.
		instance.SuspendedAt = time.Time{}
		instance.DomainBlockID = ""
		if err := p.state.DB.UpdateInstance(
			ctx,
			instance,
			"suspended_at",
			"domain_block_id",
		); err != nil {
			err = gtserror.Newf("db error updating instance with domain %s: %w", domainBlock.Domain, err)
			return nil, gtserror.NewErrorInternalError(err)
		}
	}

	// Unsuspend all accounts whose suspension origin was this domain block
	// 1. remove the 'suspended_at' entry from their accounts
	if err := p.state.DB.UpdateWhere(ctx, []db.Where{
		{Key: "suspension_origin", Value: domainBlock.ID},
	}, "suspended_at", nil, &[]*gtsmodel.Account{}); err != nil {
		err = fmt.Errorf("database error removing suspended_at from accounts: %w", err)
		return nil, gtserror.NewErrorInternalError(err)
	}aaaaaaaaaaaaaaaaaaa

	// 2. remove the 'suspension_origin' entry from their accounts
	if err := p.state.DB.UpdateWhere(ctx, []db.Where{
		{Key: "suspension_origin", Value: domainBlock.ID},
	}, "suspension_origin", nil, &[]*gtsmodel.Account{}); err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("database error removing suspension_origin from accounts: %s", err))
	}

	return apiDomainBlock, nil
}

// stubbifyInstance renders the given instance as a stub,
// removing most information from it and marking it as
// suspended.
//
// For caller's convenience, this function returns the db
// names of all columns that are updated by it.
func stubbifyInstance(instance *gtsmodel.Instance, domainBlockID string) []string {
	instance.Title = ""
	instance.SuspendedAt = time.Now()
	instance.DomainBlockID = domainBlockID
	instance.ShortDescription = ""
	instance.Description = ""
	instance.Terms = ""
	instance.ContactEmail = ""
	instance.ContactAccountUsername = ""
	instance.ContactAccountID = ""
	instance.Version = ""

	return []string{
		"title",
		"suspended_at",
		"domain_block_id",
		"short_description",
		"description",
		"terms",
		"contact_email",
		"contact_account_username",
		"contact_account_id",
		"version",
	}
}

func (p *Processor) apiDomainBlock(ctx context.Context, domainBlock *gtsmodel.DomainBlock) (*apimodel.DomainBlock, gtserror.WithCode) {
	apiDomainBlock, err := p.tc.DomainBlockToAPIDomainBlock(ctx, domainBlock, false)
	if err != nil {
		err = gtserror.Newf("error converting domain block for %s to api model : %w", domainBlock.Domain, err)
		gtserror.NewErrorInternalError(err)
	}

	return apiDomainBlock, nil
}
