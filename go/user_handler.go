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
	"os/exec"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultSessionIDKey      = "SESSIONID"
	defaultSessionExpiresKey = "EXPIRES"
	defaultUserIDKey         = "USERID"
	defaultUsernameKey       = "USERNAME"
	bcryptDefaultCost        = bcrypt.MinCost
)

var fallbackImage = "../img/NoImage.jpg"

type UserModel struct {
	ID             int64  `db:"id"`
	Name           string `db:"name"`
	DisplayName    string `db:"display_name"`
	Description    string `db:"description"`
	HashedPassword string `db:"password"`
}

type User struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Theme       Theme  `json:"theme,omitempty"`
	IconHash    string `json:"icon_hash,omitempty"`
}

type Theme struct {
	ID       int64 `json:"id"`
	DarkMode bool  `json:"dark_mode"`
}

type ThemeModel struct {
	ID       int64 `db:"id"`
	UserID   int64 `db:"user_id"`
	DarkMode bool  `db:"dark_mode"`
}

type PostUserRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	// Password is non-hashed password.
	Password string               `json:"password"`
	Theme    PostUserRequestTheme `json:"theme"`
}

type PostUserRequestTheme struct {
	DarkMode bool `json:"dark_mode"`
}

type LoginRequest struct {
	Username string `json:"username"`
	// Password is non-hashed password.
	Password string `json:"password"`
}

type PostIconRequest struct {
	Image []byte `json:"image"`
}

type PostIconResponse struct {
	ID int64 `json:"id"`
}

func getIconHandler(c echo.Context) error {
	ctx := c.Request().Context()

	username := c.Param("username")

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var user UserModel
	if err := tx.GetContext(ctx, &user, "SELECT * FROM users WHERE name = ?", username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "not found user that has the given username")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user: "+err.Error())
	}

	var image []byte
	if err := tx.GetContext(ctx, &image, "SELECT image FROM icons WHERE user_id = ?", user.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.File(fallbackImage)
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user icon: "+err.Error())
		}
	}

	return c.Blob(http.StatusOK, "image/jpeg", image)
}

func postIconHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		return err
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	var req *PostIconRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM icons WHERE user_id = ?", userID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete old user icon: "+err.Error())
	}

	rs, err := tx.ExecContext(ctx, "INSERT INTO icons (user_id, image) VALUES (?, ?)", userID, req.Image)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert new user icon: "+err.Error())
	}

	iconID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted icon id: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, &PostIconResponse{
		ID: iconID,
	})
}

func getMeHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	sess, _ := session.Get(defaultSessionIDKey, c)
	userID := sess.Values[defaultUserIDKey].(int64)

	user, err := fetchUserDetailsByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "not found user that has the userid in session")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch user details: "+err.Error())
	}

	return c.JSON(http.StatusOK, user)
}

// ユーザ登録API
// POST /api/register
func registerHandler(c echo.Context) error {
	ctx := c.Request().Context()
	defer c.Request().Body.Close()

	req := PostUserRequest{}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	if req.Name == "pipe" {
		return echo.NewHTTPError(http.StatusBadRequest, "the username 'pipe' is reserved")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptDefaultCost)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate hashed password: "+err.Error())
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	userModel := UserModel{
		Name:           req.Name,
		DisplayName:    req.DisplayName,
		Description:    req.Description,
		HashedPassword: string(hashedPassword),
	}

	result, err := tx.NamedExecContext(ctx, "INSERT INTO users (name, display_name, description, password) VALUES(:name, :display_name, :description, :password)", userModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert user: "+err.Error())
	}

	userID, err := result.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted user id: "+err.Error())
	}

	themeModel := ThemeModel{
		UserID:   userID,
		DarkMode: req.Theme.DarkMode,
	}

	if _, err := tx.NamedExecContext(ctx, "INSERT INTO themes (user_id, dark_mode) VALUES(:user_id, :dark_mode)", themeModel); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert user theme: "+err.Error())
	}

	if out, err := exec.Command("pdnsutil", "add-record", "u.isucon.local", req.Name, "A", "0", powerDNSSubdomainAddress).CombinedOutput(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, string(out)+": "+err.Error())
	}

	userModel.ID = userID
	user, err := fillUserResponseForRegisterHandler(ctx, tx, userModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill user: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, user)
}

// ユーザログインAPI
// POST /api/login
func loginHandler(c echo.Context) error {
	ctx := c.Request().Context()
	defer c.Request().Body.Close()

	req := LoginRequest{}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	userModel := UserModel{}
	// usernameはUNIQUEなので、whereで一意に特定できる
	err = tx.GetContext(ctx, &userModel, "SELECT * FROM users WHERE name = ?", req.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid username or password")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	err = bcrypt.CompareHashAndPassword([]byte(userModel.HashedPassword), []byte(req.Password))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid username or password")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to compare hash and password: "+err.Error())
	}

	sessionEndAt := time.Now().Add(1 * time.Hour)

	sessionID := uuid.NewString()

	sess, err := session.Get(defaultSessionIDKey, c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "failed to get session")
	}

	sess.Options = &sessions.Options{
		Domain: "u.isucon.local",
		MaxAge: int(60000),
		Path:   "/",
	}
	sess.Values[defaultSessionIDKey] = sessionID
	sess.Values[defaultUserIDKey] = userModel.ID
	sess.Values[defaultUsernameKey] = userModel.Name
	sess.Values[defaultSessionExpiresKey] = sessionEndAt.Unix()

	if err := sess.Save(c.Request(), c.Response()); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to save session: "+err.Error())
	}

	return c.NoContent(http.StatusOK)
}

// / ユーザ詳細API
// GET /api/user/:username
func getUserHandler(c echo.Context) error {
	ctx := c.Request().Context()
	if err := verifyUserSession(c); err != nil {
		return err
	}

	username := c.Param("username")

	user, err := fetchUserDetailsByName(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "not found user that has the given username")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch user details: "+err.Error())
	}

	return c.JSON(http.StatusOK, user)
}

func verifyUserSession(c echo.Context) error {
	sess, err := session.Get(defaultSessionIDKey, c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "failed to get session")
	}

	sessionExpires, ok := sess.Values[defaultSessionExpiresKey]
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "failed to get EXPIRES value from session")
	}

	_, ok = sess.Values[defaultUserIDKey].(int64)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "failed to get USERID value from session")
	}

	now := time.Now()
	if now.Unix() > sessionExpires.(int64) {
		return echo.NewHTTPError(http.StatusUnauthorized, "session has expired")
	}

	return nil
}

func fillUserResponse(ctx context.Context, tx *sqlx.Tx, userModel UserModel) (User, error) {
	themeModel := ThemeModel{}
	if err := tx.GetContext(ctx, &themeModel, "SELECT * FROM themes WHERE user_id = ?", userModel.ID); err != nil {
		return User{}, err
	}

	var image []byte
	if err := tx.GetContext(ctx, &image, "SELECT image FROM icons WHERE user_id = ?", userModel.ID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return User{}, err
		}
		image, err = os.ReadFile(fallbackImage)
		if err != nil {
			return User{}, err
		}
	}
	iconHash := sha256.Sum256(image)

	user := User{
		ID:          userModel.ID,
		Name:        userModel.Name,
		DisplayName: userModel.DisplayName,
		Description: userModel.Description,
		Theme: Theme{
			ID:       themeModel.ID,
			DarkMode: themeModel.DarkMode,
		},
		IconHash: fmt.Sprintf("%x", iconHash),
	}

	return user, nil
}

func fetchUserDetailsByName(ctx context.Context, username string) (User, error) {
	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var user User
	query := `
	SELECT u.id, u.name, u.display_name, u.description, t.id, t.dark_mode, COALESCE(i.image, '') as image
	FROM users u
	LEFT JOIN themes t ON u.id = t.user_id
	LEFT JOIN icons i ON u.id = i.user_id
	WHERE u.name = ?
	`

	row := tx.QueryRowxContext(ctx, query, username)
	var image []byte
	if err := row.Scan(&user.ID, &user.Name, &user.DisplayName, &user.Description, &user.Theme.ID, &user.Theme.DarkMode, &image); err != nil {
		return User{}, fmt.Errorf("failed to scan user details: %w", err)
	}

	if len(image) == 0 {
		image, err = os.ReadFile(fallbackImage)
		if err != nil {
			return User{}, fmt.Errorf("failed to read fallback image: %w", err)
		}
	}

	iconHash := sha256.Sum256(image)
	user.IconHash = fmt.Sprintf("%x", iconHash)

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("failed to commit: %w", err)
	}

	return user, nil
}

func fillUserResponseForRegisterHandler(ctx context.Context, tx *sqlx.Tx, userModel UserModel) (User, error) {
	themeModel := ThemeModel{}
	var image []byte

	// Fetch theme and icon in a single query
	query := `
		SELECT t.id, t.dark_mode, COALESCE(i.image, '') AS image 
		FROM themes t
		LEFT JOIN icons i ON t.user_id = i.user_id
		WHERE t.user_id = ?
	`

	err := tx.QueryRowxContext(ctx, query, userModel.ID).Scan(&themeModel.ID, &themeModel.DarkMode, &image)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return User{}, err
		}
		image, err = os.ReadFile(fallbackImage)
		if err != nil {
			return User{}, err
		}
	}

	iconHash := sha256.Sum256(image)

	user := User{
		ID:          userModel.ID,
		Name:        userModel.Name,
		DisplayName: userModel.DisplayName,
		Description: userModel.Description,
		Theme: Theme{
			ID:       themeModel.ID,
			DarkMode: themeModel.DarkMode,
		},
		IconHash: fmt.Sprintf("%x", iconHash),
	}

	return user, nil
}

func fetchUserDetailsByID(ctx context.Context, userID int64) (User, error) {
	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var user User
	query := `
		SELECT u.id, u.name, u.display_name, u.description, t.id, t.dark_mode, COALESCE(i.image, '') as image
		FROM users u
		LEFT JOIN themes t ON u.id = t.user_id
		LEFT JOIN icons i ON u.id = i.user_id
		WHERE u.id = ?
	`

	row := tx.QueryRowxContext(ctx, query, userID)
	var image []byte
	if err := row.Scan(&user.ID, &user.Name, &user.DisplayName, &user.Description, &user.Theme.ID, &user.Theme.DarkMode, &image); err != nil {
		return User{}, fmt.Errorf("failed to scan user details: %w", err)
	}

	if len(image) == 0 {
		image, err = os.ReadFile(fallbackImage)
		if err != nil {
			return User{}, fmt.Errorf("failed to read fallback image: %w", err)
		}
	}

	iconHash := sha256.Sum256(image)
	user.IconHash = fmt.Sprintf("%x", iconHash)

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("failed to commit: %w", err)
	}

	return user, nil
}

func fetchUserDetailsByIDForReaction(ctx context.Context, userID int64) (User, error) {
	type UserResponseModel struct {
		UserModel
		ThemeID  int64  `db:"theme_id"`
		DarkMode bool   `db:"dark_mode"`
		Icon     []byte `db:"icon"`
	}
	var userResponse UserResponseModel
	query := `
		SELECT u.*, t.id AS theme_id, t.dark_mode, COALESCE(i.image, '') AS icon
		FROM users u
		LEFT JOIN themes t ON u.id = t.user_id
		LEFT JOIN icons i ON u.id = i.user_id
		WHERE u.id = ?
	`
	if err := dbConn.GetContext(ctx, &userResponse, query, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, err
		}
		image, err := os.ReadFile(fallbackImage)
		if err != nil {
			return User{}, err
		}
		userResponse.Icon = image
	}

	iconHash := sha256.Sum256(userResponse.Icon)

	user := User{
		ID:          userResponse.ID,
		Name:        userResponse.Name,
		DisplayName: userResponse.DisplayName,
		Description: userResponse.Description,
		Theme: Theme{
			ID:       userResponse.ThemeID,
			DarkMode: userResponse.DarkMode,
		},
		IconHash: fmt.Sprintf("%x", iconHash),
	}

	return user, nil
}
