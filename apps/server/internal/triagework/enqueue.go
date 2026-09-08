package triagework

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func enqueueWork(ctx context.Context, tx pgx.Tx, ev *Evidence, owner, ticket, author string) (*Item, error) {
	// Enqueue and recurrence decisions serialize by logical identity, including
	// the moment when a resolved item gives way to a new independent repair cycle.
	identity, _ := json.Marshal([]string{ev.Repository, ev.Framework, ev.Name, ev.StableKey})
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(identity)); err != nil {
		return nil, err
	}
	previous, err := scanItem(tx.QueryRow(ctx, `SELECT `+itemColumns+` FROM triage_repairs r WHERE repository=$1 AND framework=$2 AND name=$3 AND stable_key=$4 ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE OF r`, ev.Repository, ev.Framework, ev.Name, ev.StableKey))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if previous != nil {
		if previous.File != ev.File || previous.FullTitle != ev.FullTitle || previous.Project != ev.Project {
			return nil, conflict("stable key changed logical test identity across reports")
		}
		switch previous.State {
		case stateResolved:
			var afterResolution bool
			if err = tx.QueryRow(ctx, `SELECT g.created_at>resolution.created_at FROM report_groups g,triage_repair_resolutions resolution WHERE g.id=$1 AND resolution.repair_id=$2`, ev.ReportGroupID, previous.ID).Scan(&afterResolution); err != nil {
				return nil, err
			}
			if !afterResolution {
				return nil, conflict("a new repair cycle requires a verified master failure observed after human resolution")
			}
		case stateProductSuspect:
			return observeProduct(ctx, tx, previous, ev, author)
		default:
			return previous, nil
		}
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO triage_repairs(repository,framework,name,stable_key,report_group_id,evidence,owner,ticket,author) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, ev.Repository, ev.Framework, ev.Name, ev.StableKey, ev.ReportGroupID, data, owner, ticket, author).Scan(&id)
	if err != nil {
		return nil, err
	}
	return lockedItem(ctx, tx, id)
}

func observeProduct(ctx context.Context, tx pgx.Tx, item *Item, ev *Evidence, author string) (*Item, error) {
	if item.ReportGroupID == ev.ReportGroupID {
		return item, nil
	}
	var newer bool
	if err := tx.QueryRow(ctx, `SELECT new.created_at>old.created_at FROM report_groups new,report_groups old WHERE new.id=$1 AND old.id=$2`, ev.ReportGroupID, item.ReportGroupID).Scan(&newer); err != nil {
		return nil, err
	}
	if !newer {
		return nil, conflict("product recurrence requires a strictly newer verified master report")
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO triage_product_observations(repair_id,report_group_id,evidence,author) VALUES($1,$2,$3,$4)`, item.ID, ev.ReportGroupID, data, author); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE triage_repairs SET report_group_id=$2,evidence=$3,lease_token=$4,updated_at=clock_timestamp() WHERE id=$1`, item.ID, ev.ReportGroupID, data, uuid.NewString()); err != nil {
		return nil, err
	}
	return lockedItem(ctx, tx, item.ID)
}
