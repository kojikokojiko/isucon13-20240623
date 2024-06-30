package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
)

type PostLivecommentRequest struct {
	Comment string `json:"comment"`
	Tip     int64  `json:"tip"`
}

type LivecommentModel struct {
	ID           int64  `db:"id"`
	UserID       int64  `db:"user_id"`
	LivestreamID int64  `db:"livestream_id"`
	Comment      string `db:"comment"`
	Tip          int64  `db:"tip"`
	CreatedAt    int64  `db:"created_at"`
}

type Livecomment struct {
	ID         int64      `json:"id"`
	User       User       `json:"user"`
	Livestream Livestream `json:"livestream"`
	Comment    string     `json:"comment"`
	Tip        int64      `json:"tip"`
	CreatedAt  int64      `json:"created_at"`
}

type LivecommentReport struct {
	ID          int64       `json:"id"`
	Reporter    User        `json:"reporter"`
	Livecomment Livecomment `json:"livecomment"`
	CreatedAt   int64       `json:"created_at"`
}

type LivecommentReportModel struct {
	ID            int64 `db:"id"`
	UserID        int64 `db:"user_id"`
	LivestreamID  int64 `db:"livestream_id"`
	LivecommentID int64 `db:"livecomment_id"`
	CreatedAt     int64 `db:"created_at"`
}

type ModerateRequest struct {
	NGWord string `json:"ng_word"`
}

type NGWord struct {
	ID           int64  `json:"id" db:"id"`
	UserID       int64  `json:"user_id" db:"user_id"`
	LivestreamID int64  `json:"livestream_id" db:"livestream_id"`
	Word         string `json:"word" db:"word"`
	CreatedAt    int64  `json:"created_at" db:"created_at"`
}

func getLivecommentsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	query := "SELECT * FROM livecomments WHERE livestream_id = ? ORDER BY created_at DESC"
	if c.QueryParam("limit") != "" {
		limit, err := strconv.Atoi(c.QueryParam("limit"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "limit query parameter must be integer")
		}
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	livecommentModels := []LivecommentModel{}
	err = tx.SelectContext(ctx, &livecommentModels, query, livestreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusOK, []*Livecomment{})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments: "+err.Error())
	}

	livecomments := make([]Livecomment, len(livecommentModels))
	for i := range livecommentModels {
		livecommentResponse, err := getLivecommentData(ctx, tx, livecommentModels[i].ID)
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "not found livecomment with the specified ID")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomment: "+err.Error())
		}

		livecomment, err := fillLivecommentResponse(ctx, tx, livecommentResponse)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill livecomment: "+err.Error())
		}

		livecomments[i] = livecomment
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusOK, livecomments)
}

func getNgwords(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var ngWords []*NGWord
	if err := tx.SelectContext(ctx, &ngWords, "SELECT * FROM ng_words WHERE user_id = ? AND livestream_id = ? ORDER BY created_at DESC", userID, livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, []*NGWord{})
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
		}
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusOK, ngWords)
}

func postLivecommentHandler(c echo.Context) error {
	ctx := c.Request().Context()
	defer c.Request().Body.Close()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	var req *PostLivecommentRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var livestreamModel LivestreamModel
	if err := tx.GetContext(ctx, &livestreamModel, "SELECT * FROM livestreams WHERE id = ?", livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livestream not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	// スパム判定
	var ngwords []*NGWord
	if err := tx.SelectContext(ctx, &ngwords, "SELECT id, user_id, livestream_id, word FROM ng_words WHERE user_id = ? AND livestream_id = ?", livestreamModel.UserID, livestreamModel.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
	}

	var hitSpam int
	for _, ngword := range ngwords {
		query := `
		SELECT COUNT(*)
		FROM
		(SELECT ? AS text) AS texts
		INNER JOIN
		(SELECT CONCAT('%', ?, '%')	AS pattern) AS patterns
		ON texts.text LIKE patterns.pattern;
		`
		if err := tx.GetContext(ctx, &hitSpam, query, req.Comment, ngword.Word); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get hitspam: "+err.Error())
		}
		c.Logger().Infof("[hitSpam=%d] comment = %s", hitSpam, req.Comment)
		if hitSpam >= 1 {
			return echo.NewHTTPError(http.StatusBadRequest, "このコメントがスパム判定されました")
		}
	}

	now := time.Now().Unix()
	livecommentModel := LivecommentModel{
		UserID:       userID,
		LivestreamID: int64(livestreamID),
		Comment:      req.Comment,
		Tip:          req.Tip,
		CreatedAt:    now,
	}

	rs, err := tx.NamedExecContext(ctx, "INSERT INTO livecomments (user_id, livestream_id, comment, tip, created_at) VALUES (:user_id, :livestream_id, :comment, :tip, :created_at)", livecommentModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert livecomment: "+err.Error())
	}

	livecommentID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted livecomment id: "+err.Error())
	}
	livecommentModel.ID = livecommentID

	livecommentResponse, err := getLivecommentData(ctx, tx, livecommentID)
	if errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "not found livecomment with the specified ID")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomment: "+err.Error())
	}

	livecomment, err := fillLivecommentResponse(ctx, tx, livecommentResponse)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill livecomment: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, livecomment)
}

func reportLivecommentHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	livecommentID, err := strconv.Atoi(c.Param("livecomment_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livecomment_id in path must be integer")
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var livestreamModel LivestreamModel
	if err := tx.GetContext(ctx, &livestreamModel, "SELECT * FROM livestreams WHERE id = ?", livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livestream not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	var livecommentModel LivecommentModel
	if err := tx.GetContext(ctx, &livecommentModel, "SELECT * FROM livecomments WHERE id = ?", livecommentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livecomment not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomment: "+err.Error())
		}
	}

	now := time.Now().Unix()
	reportModel := LivecommentReportModel{
		UserID:        int64(userID),
		LivestreamID:  int64(livestreamID),
		LivecommentID: int64(livecommentID),
		CreatedAt:     now,
	}
	rs, err := tx.NamedExecContext(ctx, "INSERT INTO livecomment_reports(user_id, livestream_id, livecomment_id, created_at) VALUES (:user_id, :livestream_id, :livecomment_id, :created_at)", &reportModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert livecomment report: "+err.Error())
	}
	reportID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted livecomment report id: "+err.Error())
	}
	reportModel.ID = reportID

	reportResponse, err := getLivecommentReportData(ctx, tx, reportID)
	if errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "not found livecomment report with the specified ID")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomment report: "+err.Error())
	}
	report, err := fillLivecommentReportResponse(ctx, tx, reportResponse)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill livecomment report: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, report)
}

// NGワードを登録
func moderateHandler(c echo.Context) error {
	ctx := c.Request().Context()
	defer c.Request().Body.Close()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	var req *ModerateRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	// 配信者自身の配信に対するmoderateなのかを検証
	var ownedLivestreams []LivestreamModel
	if err := tx.SelectContext(ctx, &ownedLivestreams, "SELECT * FROM livestreams WHERE id = ? AND user_id = ?", livestreamID, userID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestreams: "+err.Error())
	}
	if len(ownedLivestreams) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "A streamer can't moderate livestreams that other streamers own")
	}

	rs, err := tx.NamedExecContext(ctx, "INSERT INTO ng_words(user_id, livestream_id, word, created_at) VALUES (:user_id, :livestream_id, :word, :created_at)", &NGWord{
		UserID:       int64(userID),
		LivestreamID: int64(livestreamID),
		Word:         req.NGWord,
		CreatedAt:    time.Now().Unix(),
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert new NG word: "+err.Error())
	}

	wordID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted NG word id: "+err.Error())
	}

	var ngwords []*NGWord
	if err := tx.SelectContext(ctx, &ngwords, "SELECT * FROM ng_words WHERE livestream_id = ?", livestreamID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
	}

	// NGワードにヒットする過去の投稿も全削除する
	for _, ngword := range ngwords {
		// ライブコメント一覧取得
		var livecomments []*LivecommentModel
		if err := tx.SelectContext(ctx, &livecomments, "SELECT * FROM livecomments"); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments: "+err.Error())
		}

		for _, livecomment := range livecomments {
			query := `
			DELETE FROM livecomments
			WHERE
			id = ? AND
			livestream_id = ? AND
			(SELECT COUNT(*)
			FROM
			(SELECT ? AS text) AS texts
			INNER JOIN
			(SELECT CONCAT('%', ?, '%')	AS pattern) AS patterns
			ON texts.text LIKE patterns.pattern) >= 1;
			`
			if _, err := tx.ExecContext(ctx, query, livecomment.ID, livestreamID, livecomment.Comment, ngword.Word); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete old livecomments that hit spams: "+err.Error())
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"word_id": wordID,
	})
}

type LivecommentResponse struct {
	LivecommentID         int64  `db:"livecomment_id"`
	LivecommentUserID     int64  `db:"lc_user_id"`
	Comment               string `db:"comment"`
	Tip                   int64  `db:"tip"`
	LivecommentCreatedAt  int64  `db:"created_at"`
	UserID                int64  `db:"user_id"`
	Username              string `db:"username"`
	DisplayName           string `db:"display_name"`
	UserDescription       string `db:"user_description"`
	ThemeID               int64  `db:"theme_id"`
	DarkMode              bool   `db:"dark_mode"`
	UserImage             []byte `db:"user_image"`
	LivestreamID          int64  `db:"livestream_id"`
	LivestreamTitle       string `db:"livestream_title"`
	LivestreamDescription string `db:"livestream_description"`
	PlaylistURL           string `db:"playlist_url"`
	ThumbnailURL          string `db:"thumbnail_url"`
	StartAt               int64  `db:"start_at"`
	EndAt                 int64  `db:"end_at"`
}

func getLivecommentData(ctx context.Context, tx *sqlx.Tx, livecommentID int64) (LivecommentResponse, error) {
	var livecommentResponse LivecommentResponse
	query := `
		SELECT 
			lc.id as livecomment_id, lc.user_id as lc_user_id, lc.livestream_id, lc.comment, lc.tip, lc.created_at,
			u.id as user_id, u.name as username, u.display_name, u.description as user_description,
			t.id as theme_id, t.dark_mode, 
			COALESCE(i.image, '') as user_image,
			ls.id as livestream_id, ls.title as livestream_title, ls.description as livestream_description, 
			ls.playlist_url, ls.thumbnail_url, ls.start_at, ls.end_at,
			tags.id as tag_id,tags.name as tag_name
		FROM 
			livecomments lc
		LEFT JOIN 
			users u ON lc.user_id = u.id
		LEFT JOIN 
			themes t ON u.id = t.user_id
		LEFT JOIN 
			icons i ON u.id = i.user_id
		LEFT JOIN 
			livestreams ls ON lc.livestream_id = ls.id
		LEFT JOIN 
			livestream_tags  ON ls.id = livestream_tags.livestream_id
		LEFT JOIN 
			tags ON livestream_tags.tag_id = tags.id
		WHERE 
			lc.id = ?
	`
	err := tx.GetContext(ctx, &livecommentResponse, query, livecommentID)
	if err != nil {
		return LivecommentResponse{}, err
	}
	return livecommentResponse, nil
}

func fillLivecommentResponse(ctx context.Context, tx *sqlx.Tx, livecommentResponse LivecommentResponse) (Livecomment, error) {
	image := livecommentResponse.UserImage
	if len(image) == 0 {
		var err error
		image, err = os.ReadFile(fallbackImage)
		if err != nil {
			return Livecomment{}, err
		}
	}
	iconHash := sha256.Sum256(image)

	commentOwner := User{
		ID:          livecommentResponse.UserID,
		Name:        livecommentResponse.Username,
		DisplayName: livecommentResponse.DisplayName,
		Description: livecommentResponse.UserDescription,
		Theme: Theme{
			ID:       livecommentResponse.ThemeID,
			DarkMode: livecommentResponse.DarkMode,
		},
		IconHash: fmt.Sprintf("%x", iconHash),
	}

	livestream := Livestream{
		ID:           livecommentResponse.LivestreamID,
		Owner:        commentOwner,
		Title:        livecommentResponse.LivestreamTitle,
		Description:  livecommentResponse.LivestreamDescription,
		PlaylistUrl:  livecommentResponse.PlaylistURL,
		ThumbnailUrl: livecommentResponse.ThumbnailURL,
		StartAt:      livecommentResponse.StartAt,
		EndAt:        livecommentResponse.EndAt,
	}

	livecomment := Livecomment{
		ID:         livecommentResponse.LivecommentID,
		User:       commentOwner,
		Livestream: livestream,
		Comment:    livecommentResponse.Comment,
		Tip:        livecommentResponse.Tip,
		CreatedAt:  livecommentResponse.LivecommentCreatedAt,
	}

	return livecomment, nil
}

type LivecommentReportResponse struct {
	ReportID              int64  `db:"report_id"`
	ReporterUserID        int64  `db:"reporter_user_id"`
	ReportCreatedAt       int64  `db:"report_created_at"`
	ReporterID            int64  `db:"reporter_id"`
	ReporterName          string `db:"reporter_name"`
	ReporterDisplayName   string `db:"reporter_display_name"`
	ReporterDescription   string `db:"reporter_description"`
	ReporterThemeID       int64  `db:"reporter_theme_id"`
	ReporterDarkMode      bool   `db:"reporter_dark_mode"`
	ReporterImage         []byte `db:"reporter_image"`
	LivecommentID         int64  `db:"livecomment_id"`
	LivecommentUserID     int64  `db:"lc_user_id"`
	Comment               string `db:"comment"`
	Tip                   int64  `db:"tip"`
	LivecommentCreatedAt  int64  `db:"livecomment_created_at"`
	LCUserID              int64  `db:"livecomment_user_id"`
	LCUsername            string `db:"lc_username"`
	LCUserDisplayName     string `db:"lc_display_name"`
	LCUserDescription     string `db:"lc_user_description"`
	LCUserThemeID         int64  `db:"lc_user_theme_id"`
	LCUserDarkMode        bool   `db:"lc_user_dark_mode"`
	LCUserImage           []byte `db:"lc_user_image"`
	LivestreamID          int64  `db:"livestream_id"`
	LivestreamTitle       string `db:"livestream_title"`
	LivestreamDescription string `db:"livestream_description"`
	PlaylistURL           string `db:"playlist_url"`
	ThumbnailURL          string `db:"thumbnail_url"`
	StartAt               int64  `db:"start_at"`
	EndAt                 int64  `db:"end_at"`
}

func getLivecommentReportData(ctx context.Context, tx *sqlx.Tx, reportID int64) (LivecommentReportResponse, error) {
	var reportResponse LivecommentReportResponse
	query := `
		SELECT 
			lr.id as report_id, lr.user_id as reporter_user_id, lr.livecomment_id, lr.created_at as report_created_at,
			ru.id as reporter_id, ru.name as reporter_name, ru.display_name as reporter_display_name, ru.description as reporter_description,
			rt.id as reporter_theme_id, rt.dark_mode as reporter_dark_mode, 
			COALESCE(ri.image, '') as reporter_image,
			lc.id as livecomment_id, lc.user_id as lc_user_id, lc.livestream_id, lc.comment, lc.tip, lc.created_at as livecomment_created_at,
			lu.id as livecomment_user_id, lu.name as lc_username, lu.display_name as lc_display_name, lu.description as lc_user_description,
			lt.id as lc_user_theme_id, lt.dark_mode as lc_user_dark_mode, 
			COALESCE(li.image, '') as lc_user_image,
			ls.id as livestream_id, ls.title as livestream_title, ls.description as livestream_description, 
			ls.playlist_url, ls.thumbnail_url, ls.start_at, ls.end_at
		FROM 
			livecomment_reports lr
		LEFT JOIN 
			users ru ON lr.user_id = ru.id
		LEFT JOIN 
			themes rt ON ru.id = rt.user_id
		LEFT JOIN 
			icons ri ON ru.id = ri.user_id
		LEFT JOIN 
			livecomments lc ON lr.livecomment_id = lc.id
		LEFT JOIN 
			users lu ON lc.user_id = lu.id
		LEFT JOIN 
			themes lt ON lu.id = lt.user_id
		LEFT JOIN 
			icons li ON lu.id = li.user_id
		LEFT JOIN 
			livestreams ls ON lc.livestream_id = ls.id
		WHERE 
			lr.id = ?
	`
	err := tx.GetContext(ctx, &reportResponse, query, reportID)
	if err != nil {
		return LivecommentReportResponse{}, err
	}
	return reportResponse, nil
}

func fillLivecommentReportResponse(ctx context.Context, tx *sqlx.Tx, reportResponse LivecommentReportResponse) (LivecommentReport, error) {
	reporterImage := reportResponse.ReporterImage
	if len(reporterImage) == 0 {
		var err error
		reporterImage, err = os.ReadFile(fallbackImage)
		if err != nil {
			return LivecommentReport{}, err
		}
	}
	reporterIconHash := sha256.Sum256(reporterImage)

	reporter := User{
		ID:          reportResponse.ReporterID,
		Name:        reportResponse.ReporterName,
		DisplayName: reportResponse.ReporterDisplayName,
		Description: reportResponse.ReporterDescription,
		Theme: Theme{
			ID:       reportResponse.ReporterThemeID,
			DarkMode: reportResponse.ReporterDarkMode,
		},
		IconHash: fmt.Sprintf("%x", reporterIconHash),
	}

	lcUserImage := reportResponse.LCUserImage
	if len(lcUserImage) == 0 {
		var err error
		lcUserImage, err = os.ReadFile(fallbackImage)
		if err != nil {
			return LivecommentReport{}, err
		}
	}
	lcUserIconHash := sha256.Sum256(lcUserImage)

	commentOwner := User{
		ID:          reportResponse.LCUserID,
		Name:        reportResponse.LCUsername,
		DisplayName: reportResponse.LCUserDisplayName,
		Description: reportResponse.LCUserDescription,
		Theme: Theme{
			ID:       reportResponse.LCUserThemeID,
			DarkMode: reportResponse.LCUserDarkMode,
		},
		IconHash: fmt.Sprintf("%x", lcUserIconHash),
	}

	livestream := Livestream{
		ID:           reportResponse.LivestreamID,
		Owner:        commentOwner,
		Title:        reportResponse.LivestreamTitle,
		Description:  reportResponse.LivestreamDescription,
		PlaylistUrl:  reportResponse.PlaylistURL,
		ThumbnailUrl: reportResponse.ThumbnailURL,
		StartAt:      reportResponse.StartAt,
		EndAt:        reportResponse.EndAt,
	}

	livecomment := Livecomment{
		ID:         reportResponse.LivecommentID,
		User:       commentOwner,
		Livestream: livestream,
		Comment:    reportResponse.Comment,
		Tip:        reportResponse.Tip,
		CreatedAt:  reportResponse.LivecommentCreatedAt,
	}

	report := LivecommentReport{
		ID:          reportResponse.ReportID,
		Reporter:    reporter,
		Livecomment: livecomment,
		CreatedAt:   reportResponse.ReportCreatedAt,
	}

	return report, nil
}
