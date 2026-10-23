package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/VargaFoundation/trica/internal/place"
)

// checkPlacements opens a location_conflict on every device this batch touched
// whose places disagree on the site, and resolves it once they agree (ADR 0061).
// The devices checked are the ones whose located_in or mounted_in changed, and
// the ones placed under an object that moved (a room under another site).
func (r *run) checkPlacements(ctx context.Context) error {
	cand := map[string]bool{}
	for k := range r.touchedRels {
		if k.Type == place.LocatedIn || k.Type == place.MountedIn {
			cand[k.From] = true
		}
	}
	if len(r.moved) > 0 {
		moved := make([]string, 0, len(r.moved))
		for id := range r.moved {
			moved = append(moved, id)
		}
		under, err := place.PlacedUnder(ctx, r.tx, moved)
		if err != nil {
			return err
		}
		for _, id := range under {
			cand[id] = true
		}
	}
	if len(cand) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cand))
	for id := range cand {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	conflicts, err := place.Conflicts(ctx, r.tx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		c, in := conflicts[id]
		if !in {
			if err := ResolveOthers(ctx, r.tx, KindLocationConflict, id, "", "reconcile", r.now); err != nil {
				return err
			}
			continue
		}
		fp := locationFingerprint(id, c)
		if err := ResolveOthers(ctx, r.tx, KindLocationConflict, id, fp, "reconcile", r.now); err != nil {
			return err
		}
		details, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if err := OpenDiscrepancy(ctx, r.tx, Discrepancy{Kind: KindLocationConflict, Fingerprint: fp, ObjectID: id,
			Key: conflictKey(c.Reason), Details: details}, r.now); err != nil {
			return err
		}
		switch c.Reason {
		case place.ReasonRack:
			r.warn("located_in: %s is placed in another site than its rack: the rack decides (location_conflict)", id)
		case place.ReasonRacks:
			r.warn("mounted_in: %s is mounted in racks of different sites: it has no site until one is withdrawn (location_conflict)", id)
		default:
			r.warn("located_in: the places of %s are in different sites: it has no site until they agree (location_conflict)", id)
		}
	}
	return nil
}

// conflictKey is the relation a person withdraws to end the conflict.
func conflictKey(reason string) string {
	if reason == place.ReasonRacks {
		return place.MountedIn
	}
	return place.LocatedIn
}

// locationFingerprint identifies a conflict by its device, its reason and the
// sites involved: places changing within the same sites refresh the same row,
// a new site or a new reason is a new discrepancy.
func locationFingerprint(device string, c place.Conflict) string {
	sum := sha256.Sum256([]byte(c.Reason + "|" + strings.Join(c.Sites, ",")))
	return KindLocationConflict + ":" + device + ":" + hex.EncodeToString(sum[:8])
}
