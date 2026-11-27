package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// rawFlowsKept is how long per-minute rows stay in flows at least. Compact
// rolls up only the hours that ended before now minus rawFlowsKept, so the
// last hour always has per-minute rows, wherever in the hour compaction ran:
// a sliding window of up to an hour (such as an alert rule's) never mixes
// in an hourly row that covers time before the window.
const rawFlowsKept = time.Hour

// Compact rolls the per-minute rows of complete hours up into flows_hourly,
// one row per flow and hour, and with hourly set, the hourly rows of UTC days
// that ended more than hourly ago into flows_daily, one row per flow and day.
// It deletes the hourly and daily rows older than retention (zero keeps them
// forever). The previous hour stays per minute along with the current one
// (see rawFlowsKept).
//
// It runs in one transaction. Rows the collector inserts meanwhile belong
// to the current hour, or are not visible to the transaction yet; either
// way they stay in flows for the next run.
func Compact(db *sql.DB, now time.Time, retention, hourly time.Duration) (CompactResult, error) {
	var result CompactResult
	cutoff := now.Add(-rawFlowsKept).Truncate(time.Hour)

	tx, err := db.Begin()
	if err != nil {
		return result, fmt.Errorf("compact flows: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`
		INSERT INTO flows_hourly
			(src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
			 in_bytes, in_packets, out_bytes, out_packets, created_at)
		SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
		       sum(in_bytes), sum(in_packets), sum(out_bytes), sum(out_packets),
		       `+hourBucket+` AS bucket
		FROM flows
		WHERE created_at < ?::TIMESTAMPTZ
		GROUP BY src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, bucket
	`, cutoff)
	if err != nil {
		return result, fmt.Errorf("roll up flows: %w", err)
	}
	result.RolledUp, _ = res.RowsAffected()

	res, err = tx.Exec(`DELETE FROM flows WHERE created_at < ?::TIMESTAMPTZ`, cutoff)
	if err != nil {
		return result, fmt.Errorf("delete rolled up flows: %w", err)
	}
	result.Removed, _ = res.RowsAffected()

	if hourly > 0 {
		// Whole UTC days only, so that a day is either hourly or daily.
		dayCutoff := now.UTC().Add(-hourly).Truncate(24 * time.Hour)

		res, err = tx.Exec(`
			INSERT INTO flows_daily
				(src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
				 in_bytes, in_packets, out_bytes, out_packets, created_at)
			SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
			       sum(in_bytes), sum(in_packets), sum(out_bytes), sum(out_packets),
			       `+dayBucket+` AS bucket
			FROM flows_hourly
			WHERE created_at < ?::TIMESTAMPTZ
			GROUP BY src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, bucket
		`, dayCutoff)
		if err != nil {
			return result, fmt.Errorf("roll up hourly flows: %w", err)
		}
		result.RolledUpDaily, _ = res.RowsAffected()

		res, err = tx.Exec(`DELETE FROM flows_hourly WHERE created_at < ?::TIMESTAMPTZ`, dayCutoff)
		if err != nil {
			return result, fmt.Errorf("delete rolled up hourly flows: %w", err)
		}
		result.RemovedHourly, _ = res.RowsAffected()
	}

	if retention > 0 {
		for _, table := range []string{"flows_hourly", "flows_daily"} {
			res, err = tx.Exec(`DELETE FROM `+table+` WHERE created_at < ?::TIMESTAMPTZ`, now.Add(-retention))
			if err != nil {
				return result, fmt.Errorf("delete expired flows: %w", err)
			}
			expired, _ := res.RowsAffected()
			result.Expired += expired
		}
	}

	if err := tx.Commit(); err != nil {
		return CompactResult{}, fmt.Errorf("compact flows: %w", err)
	}

	return result, nil
}
