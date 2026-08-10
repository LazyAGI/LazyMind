package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/store"
)

type RouterTrafficRange struct {
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Granularity string `json:"granularity" enum:"hour,day"`
}

type RouterTrafficSummary struct {
	AnswerCount       int64   `json:"answer_count"`
	UserCount         int     `json:"user_count"`
	ConversationCount int     `json:"conversation_count"`
	FeedbackCount     int64   `json:"feedback_count"`
	FeedbackRate      float64 `json:"feedback_rate"`
}

type RouterAlgorithmTraffic struct {
	AlgorithmID       string   `json:"algorithm_id"`
	AnswerCount       int64    `json:"answer_count"`
	ActualRatio       float64  `json:"actual_ratio"`
	UserCount         int      `json:"user_count"`
	ConversationCount int      `json:"conversation_count"`
	LikeCount         int64    `json:"like_count"`
	DislikeCount      int64    `json:"dislike_count"`
	FeedbackRate      float64  `json:"feedback_rate"`
	PositiveRate      *float64 `json:"positive_rate" required:"true" nullable:"true"`
}

type RouterTrafficPoint struct {
	Time   string           `json:"time"`
	Counts map[string]int64 `json:"counts" required:"true"`
}

type RouterDislikeReason struct {
	AlgorithmID string  `json:"algorithm_id"`
	Reason      string  `json:"reason"`
	Count       int64   `json:"count"`
	Ratio       float64 `json:"ratio"`
}

type RouterTrafficStatsResponse struct {
	Range          RouterTrafficRange       `json:"range"`
	Summary        RouterTrafficSummary     `json:"summary"`
	Algorithms     []RouterAlgorithmTraffic `json:"algorithms" required:"true"`
	Trend          []RouterTrafficPoint     `json:"trend" required:"true"`
	DislikeReasons []RouterDislikeReason    `json:"dislike_reasons" required:"true"`
}

type routerTrafficRow struct {
	AlgorithmID    string
	ConversationID string
	UserID         string
	FeedBack       int
	Reason         string
	CreateTime     time.Time
	Ext            json.RawMessage
}

type routerTrafficAttempt struct {
	AlgorithmID string    `json:"algorithm_id"`
	FeedBack    int       `json:"feed_back"`
	Reason      string    `json:"reason"`
	CreateTime  time.Time `json:"create_time"`
}

type routerTrafficAggregate struct {
	answers       int64
	users         map[string]struct{}
	conversations map[string]struct{}
	likes         int64
	dislikes      int64
}

func GetRouterTrafficStats(w http.ResponseWriter, r *http.Request) {
	start, end, granularity, err := parseRouterTrafficRange(r)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	result, err := loadRouterTrafficStats(r.Context(), db, start, end, granularity)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("load router traffic stats failed: %v", err), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, result)
}

func parseRouterTrafficRange(r *http.Request) (time.Time, time.Time, string, error) {
	query := r.URL.Query()
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(query.Get("start_time")))
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("invalid start_time")
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(query.Get("end_time")))
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("invalid end_time")
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, "", fmt.Errorf("invalid request")
	}
	granularity := strings.TrimSpace(query.Get("granularity"))
	if granularity != "hour" && granularity != "day" {
		return time.Time{}, time.Time{}, "", fmt.Errorf("invalid request")
	}
	return start.UTC(), end.UTC(), granularity, nil
}

func loadRouterTrafficStats(
	ctx context.Context,
	db *gorm.DB,
	start, end time.Time,
	granularity string,
) (RouterTrafficStatsResponse, error) {
	result := RouterTrafficStatsResponse{
		Range: RouterTrafficRange{
			StartTime: start.Format(time.RFC3339), EndTime: end.Format(time.RFC3339), Granularity: granularity,
		},
		Algorithms:     []RouterAlgorithmTraffic{},
		Trend:          []RouterTrafficPoint{},
		DislikeReasons: []RouterDislikeReason{},
	}

	var rows []routerTrafficRow
	err := db.WithContext(ctx).
		Table("chat_histories AS h").
		Select("h.algorithm_id, h.conversation_id, c.create_user_id AS user_id, h.feed_back, h.reason, h.create_time, h.ext").
		Joins("JOIN conversations AS c ON c.id = h.conversation_id").
		Where("h.algorithm_id IS NOT NULL AND h.algorithm_id <> ''").
		Where("c.is_task_conv = ? AND c.deleted_at IS NULL", false).
		Order("h.create_time, h.id").
		Scan(&rows).Error
	if err != nil {
		return result, err
	}

	overall := newRouterTrafficAggregate()
	byAlgorithm := map[string]*routerTrafficAggregate{}
	trend := map[string]map[string]int64{}
	dislikeReasons := map[string]map[string]int64{}
	for _, history := range rows {
		for _, row := range routerTrafficAttempts(history) {
			if row.CreateTime.Before(start) || !row.CreateTime.Before(end) {
				continue
			}
			algorithmID := strings.TrimSpace(row.AlgorithmID)
			if algorithmID == "" {
				continue
			}
			algorithm := byAlgorithm[algorithmID]
			if algorithm == nil {
				algorithm = newRouterTrafficAggregate()
				byAlgorithm[algorithmID] = algorithm
			}
			for _, aggregate := range []*routerTrafficAggregate{overall, algorithm} {
				aggregate.answers++
				aggregate.users[row.UserID] = struct{}{}
				aggregate.conversations[row.ConversationID] = struct{}{}
				if row.FeedBack == 1 {
					aggregate.likes++
				} else if row.FeedBack == 2 {
					aggregate.dislikes++
				}
			}

			bucket := routerTrafficBucket(row.CreateTime, granularity)
			if trend[bucket] == nil {
				trend[bucket] = map[string]int64{}
			}
			trend[bucket][algorithmID]++
			if row.FeedBack == 2 {
				reason := strings.TrimSpace(row.Reason)
				if dislikeReasons[algorithmID] == nil {
					dislikeReasons[algorithmID] = map[string]int64{}
				}
				dislikeReasons[algorithmID][reason]++
			}
		}
	}

	feedbackCount := overall.likes + overall.dislikes
	result.Summary = RouterTrafficSummary{
		AnswerCount:       overall.answers,
		UserCount:         len(overall.users),
		ConversationCount: len(overall.conversations),
		FeedbackCount:     feedbackCount,
		FeedbackRate:      routerTrafficRatio(feedbackCount, overall.answers),
	}

	algorithmIDs := make([]string, 0, len(byAlgorithm))
	for algorithmID := range byAlgorithm {
		algorithmIDs = append(algorithmIDs, algorithmID)
	}
	sort.Strings(algorithmIDs)
	for _, algorithmID := range algorithmIDs {
		aggregate := byAlgorithm[algorithmID]
		feedbackCount := aggregate.likes + aggregate.dislikes
		var positiveRate *float64
		if feedbackCount > 0 {
			value := routerTrafficRatio(aggregate.likes, feedbackCount)
			positiveRate = &value
		}
		result.Algorithms = append(result.Algorithms, RouterAlgorithmTraffic{
			AlgorithmID:       algorithmID,
			AnswerCount:       aggregate.answers,
			ActualRatio:       routerTrafficRatio(aggregate.answers, overall.answers),
			UserCount:         len(aggregate.users),
			ConversationCount: len(aggregate.conversations),
			LikeCount:         aggregate.likes,
			DislikeCount:      aggregate.dislikes,
			FeedbackRate:      routerTrafficRatio(feedbackCount, aggregate.answers),
			PositiveRate:      positiveRate,
		})
	}

	buckets := make([]string, 0, len(trend))
	for bucket := range trend {
		buckets = append(buckets, bucket)
	}
	sort.Strings(buckets)
	for _, bucket := range buckets {
		result.Trend = append(result.Trend, RouterTrafficPoint{Time: bucket, Counts: trend[bucket]})
	}

	for algorithmID, reasons := range dislikeReasons {
		for reason, count := range reasons {
			result.DislikeReasons = append(result.DislikeReasons, RouterDislikeReason{
				AlgorithmID: algorithmID,
				Reason:      reason,
				Count:       count,
				Ratio:       routerTrafficRatio(count, byAlgorithm[algorithmID].dislikes),
			})
		}
	}
	sort.Slice(result.DislikeReasons, func(i, j int) bool {
		left, right := result.DislikeReasons[i], result.DislikeReasons[j]
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		if left.AlgorithmID != right.AlgorithmID {
			return left.AlgorithmID < right.AlgorithmID
		}
		return left.Reason < right.Reason
	})
	return result, nil
}

func routerTrafficAttempts(history routerTrafficRow) []routerTrafficRow {
	var ext struct {
		Attempts []routerTrafficAttempt `json:"router_traffic_attempts"`
	}
	_ = json.Unmarshal(history.Ext, &ext)
	rows := make([]routerTrafficRow, 0, len(ext.Attempts)+1)
	for _, attempt := range ext.Attempts {
		rows = append(rows, routerTrafficRow{
			AlgorithmID:    attempt.AlgorithmID,
			ConversationID: history.ConversationID,
			UserID:         history.UserID,
			FeedBack:       attempt.FeedBack,
			Reason:         attempt.Reason,
			CreateTime:     attempt.CreateTime,
		})
	}
	return append(rows, history)
}

func newRouterTrafficAggregate() *routerTrafficAggregate {
	return &routerTrafficAggregate{users: map[string]struct{}{}, conversations: map[string]struct{}{}}
}

func routerTrafficBucket(value time.Time, granularity string) string {
	value = value.UTC()
	if granularity == "hour" {
		return value.Truncate(time.Hour).Format(time.RFC3339)
	}
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

func routerTrafficRatio(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
