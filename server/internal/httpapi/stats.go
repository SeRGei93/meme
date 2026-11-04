package httpapi

import (
	"net/http"
	"time"
)

const statsPath = "/api/v1/stats"

type statsResponse struct {
	Records      recordStatsResponse   `json:"records"`
	Projects     int                   `json:"projects"`
	Clients      int                   `json:"clients"`
	Jobs         jobCountsResponse     `json:"jobs"`
	LastIngestAt *time.Time            `json:"last_ingest_at"`
	Database     databaseStatsResponse `json:"database"`
}

type recordStatsResponse struct {
	Total        int                      `json:"total"`
	Final        int                      `json:"final"`
	Draft        int                      `json:"draft"`
	BySource     sourceCountsResponse     `json:"by_source"`
	ByCardStatus cardStatusCountsResponse `json:"by_card_status"`
}

type sourceCountsResponse struct {
	OpenCode int `json:"opencode"`
	Jarvis   int `json:"jarvis"`
}

type cardStatusCountsResponse struct {
	Heuristic int `json:"heuristic"`
	Queued    int `json:"queued"`
	OK        int `json:"ok"`
	Failed    int `json:"failed"`
}

type jobCountsResponse struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

type databaseStatsResponse struct {
	Status        string `json:"status"`
	SchemaVersion int    `json:"schema_version"`
	SizeBytes     int64  `json:"size_bytes"`
}

func (s *server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		s.logger.Error("read stats",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, statsResponse{
		Records: recordStatsResponse{
			Total: stats.Records.Total,
			Final: stats.Records.Final,
			Draft: stats.Records.Draft,
			BySource: sourceCountsResponse{
				OpenCode: stats.Records.BySource.OpenCode,
				Jarvis:   stats.Records.BySource.Jarvis,
			},
			ByCardStatus: cardStatusCountsResponse{
				Heuristic: stats.Records.ByCardStatus.Heuristic,
				Queued:    stats.Records.ByCardStatus.Queued,
				OK:        stats.Records.ByCardStatus.OK,
				Failed:    stats.Records.ByCardStatus.Failed,
			},
		},
		Projects:     stats.Projects,
		Clients:      stats.Clients,
		Jobs:         jobCountsResponse{Queued: stats.Jobs.Queued, Running: stats.Jobs.Running, Done: stats.Jobs.Done, Failed: stats.Jobs.Failed},
		LastIngestAt: stats.LastIngestAt,
		Database: databaseStatsResponse{
			Status:        stats.Database.Status,
			SchemaVersion: stats.Database.SchemaVersion,
			SizeBytes:     stats.Database.SizeBytes,
		},
	})
}
