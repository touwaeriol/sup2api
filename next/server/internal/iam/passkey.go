package iam

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Passkey is the API view of a registered passkey.
type Passkey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	RPID       string     `json:"rp_id"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// PasskeyCeremony is the response of the two begin endpoints: the options for
// the browser's WebAuthn call.
type PasskeyCeremony struct {
	FlowToken string `json:"flow_token"`
	ExpiresIn int64  `json:"expires_in"`
	Options   any    `json:"options"`
}

var errPasskeyUnavailable = core.ErrInvalidArgument.WithMessage("passkeys are not available on this address")
var errPasskeyFailed = core.ErrUnauthenticated.WithMessage("passkey verification failed")

// webAuthnFor builds the relying party of a request. SUB2API_WEBAUTHN_RP_ID
// names it and the browser's Origin must be that domain or below it. Without
// the setting it is the host the console was opened on, which the Origin must
// name; a failover proxy that rewrites Host therefore needs the setting. The
// browser signs the origin it really is on, and that is what the library
// compares with this one, so a request with a forged Origin header gets no
// further than one with a true one.
func (s *Service) webAuthnFor(r *http.Request, rpID string) (*webauthn.WebAuthn, error) {
	origin, rpID, err := s.relyingParty(r, rpID)
	if err != nil {
		return nil, err
	}
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: totpIssuer,
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
	})
}

// relyingParty checks the Origin of r against rpID (the setting when "",
// then the Origin's host) as webAuthnFor describes, and returns the origin
// and the relying party's id.
func (s *Service) relyingParty(r *http.Request, rpID string) (origin, id string, err error) {
	origin = r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if origin == "" || err != nil || u.Hostname() == "" {
		return "", "", errPasskeyUnavailable
	}
	host := u.Hostname()
	if rpID == "" {
		rpID = s.cfg.WebAuthnRPID
	}
	if s.cfg.WebAuthnRPID == "" && !strings.EqualFold(host, hostname(r.Host)) {
		return "", "", errPasskeyUnavailable
	}
	if rpID == "" {
		rpID = host
	}
	if !strings.EqualFold(host, rpID) && !strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(rpID)) {
		return "", "", errPasskeyUnavailable
	}
	// WebAuthn has no IP-address relying parties.
	if net.ParseIP(rpID) != nil {
		return "", "", errPasskeyUnavailable
	}
	return origin, rpID, nil
}

// hostname is hostport without its port, when it has one.
func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// webAuthnUser adapts a user to the library.
type webAuthnUser struct {
	id          int64
	email       string
	displayName string
	credentials []webauthn.Credential
}

func userHandle(id int64) []byte { return []byte(strconv.FormatInt(id, 10)) }

func (u *webAuthnUser) WebAuthnID() []byte                         { return userHandle(u.id) }
func (u *webAuthnUser) WebAuthnName() string                       { return u.email }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return strOr(u.displayName, u.email) }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func strOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) loadWebAuthnUser(ctx context.Context, userID int64) (*webAuthnUser, string, error) {
	u := &webAuthnUser{id: userID}
	var status string
	g := store.G(ctx, s.db.Gorm)
	err := liveUser(g, userID, "email, display_name, status").Row().Scan(&u.email, &u.displayName, &status)
	if store.IsNoRows(err) {
		return nil, "", errPasskeyFailed
	}
	if err != nil {
		return nil, "", err
	}
	var rows []model.UserPasskey
	if err := passkeyCredentials(g, userID).Find(&rows).Error; err != nil {
		return nil, "", err
	}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Credential, &c); err != nil {
			return nil, "", err
		}
		u.credentials = append(u.credentials, c)
	}
	return u, status, nil
}

// passkeysOf are the passkeys of userID.
func passkeysOf(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.UserPasskey{}).Where("user_id = ?", userID)
}

// passkeyCredentials reads the WebAuthn credentials of userID.
func passkeyCredentials(db *gorm.DB, userID int64) *gorm.DB {
	return passkeysOf(db, userID).Select("credential")
}

// Passkeys lists the user's passkeys.
func (s *Service) Passkeys(ctx context.Context, userID int64) ([]Passkey, error) {
	var rows []model.UserPasskey
	if err := passkeyList(store.G(ctx, s.db.Gorm), userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Passkey, 0, len(rows))
	for _, r := range rows {
		out = append(out, Passkey{ID: r.ID, Name: r.Name, RPID: r.RpID, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt})
	}
	return out, nil
}

// passkeyList is what Passkeys shows of the passkeys of userID, newest first.
func passkeyList(db *gorm.DB, userID int64) *gorm.DB {
	return passkeysOf(db, userID).Select("id, name, rp_id, created_at, last_used_at").Order("created_at DESC")
}

// normalizePasskeyName trims a passkey's name; empty means "Passkey".
func normalizePasskeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if utf8.RuneCountInString(name) > 100 {
		return "", core.InvalidFields(core.FieldError{Field: "name", Code: "too_long", Message: "at most 100 characters"})
	}
	return name, nil
}

// BeginPasskeyRegistration starts adding a passkey; the route has verified
// the caller (step-up self.passkey.add).
func (s *Service) BeginPasskeyRegistration(ctx context.Context, r *http.Request, userID int64) (*PasskeyCeremony, error) {
	wa, err := s.webAuthnFor(r, "")
	if err != nil {
		return nil, err
	}
	u, _, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(u.credentials))
	for _, c := range u.credentials {
		exclude = append(exclude, c.Descriptor())
	}
	options, session, err := wa.BeginRegistration(u, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, err
	}
	token, err := s.createFlow(ctx, flowPasskeyRegister, userID, session)
	if err != nil {
		return nil, err
	}
	return &PasskeyCeremony{FlowToken: token, ExpiresIn: flowTTLSeconds, Options: options}, nil
}

// FinishPasskeyRegistration verifies the browser's answer and stores the passkey.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, r *http.Request, userID int64, flowToken, name string, credential json.RawMessage) (*Passkey, error) {
	name, err := normalizePasskeyName(name)
	if err != nil {
		return nil, err
	}
	rpID, c, err := s.createPasskeyCredential(ctx, r, userID, flowToken, credential)
	if err != nil {
		return nil, err
	}
	return s.storePasskey(ctx, userID, name, rpID, c)
}

// createPasskeyCredential takes the flow of a BeginPasskeyRegistration
// ceremony and verifies the browser's answer to it, returning the relying
// party's id and the new credential.
func (s *Service) createPasskeyCredential(ctx context.Context, r *http.Request, userID int64, flowToken string, credential json.RawMessage) (string, *webauthn.Credential, error) {
	var session webauthn.SessionData
	if _, err := s.takeFlow(ctx, flowToken, flowPasskeyRegister, userID, &session); err != nil {
		return "", nil, err
	}
	wa, err := s.webAuthnFor(r, session.RelyingPartyID)
	if err != nil {
		return "", nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credential)
	if err != nil {
		return "", nil, core.ErrInvalidArgument.WithMessage("invalid passkey response").WithCause(err)
	}
	u, _, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	c, err := wa.CreateCredential(u, session, parsed)
	if err != nil {
		return "", nil, core.ErrInvalidArgument.WithMessage("passkey verification failed").WithCause(err)
	}
	return session.RelyingPartyID, c, nil
}

// storePasskey stores the credential c of userID as a passkey named name
// and audits it; a credential stored before is a conflict.
func (s *Service) storePasskey(ctx context.Context, userID int64, name, rpID string, c *webauthn.Credential) (*Passkey, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	p := &Passkey{Name: name, RPID: rpID}
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		err := insertPasskey(store.G(ctx, tx), userID, name, rpID, c.ID, raw).Row().Scan(&p.ID, &p.CreatedAt)
		if store.IsNoRows(err) {
			return core.ErrConflict.WithMessage("this passkey is already registered")
		}
		if err != nil {
			return err
		}
		return auditSelf(ctx, tx, userID, "user.passkey.add", map[string]any{"passkey_id": p.ID, "name": name})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// insertPasskey stores a new passkey and returns its id and created_at; a
// credential stored before returns no row. credential_id and credential are
// bytes: none of their ? follows a (.
func insertPasskey(db *gorm.DB, userID int64, name, rpID string, credentialID, credential []byte) *gorm.DB {
	row := store.OneRow(db).Select("?, ?, ?, ?, ?", userID, name, rpID, credentialID, credential)
	return gormpg.InsertSelectReturning(db, model.UserPasskey{}.TableName(), []string{"user_id", "name", "rp_id", "credential_id", "credential"}, row,
		clause.OnConflict{Columns: []clause.Column{{Name: "credential_id"}}, DoNothing: true},
		clause.Returning{Columns: []clause.Column{{Name: "id"}, {Name: "created_at"}}})
}

// RenamePasskey changes the name of one of the user's passkeys.
func (s *Service) RenamePasskey(ctx context.Context, userID, id int64, name string) (*Passkey, error) {
	name, err := normalizePasskeyName(name)
	if err != nil {
		return nil, err
	}
	p := &Passkey{ID: id}
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var row model.UserPasskey
		res := renamePasskey(store.G(ctx, tx), id, userID, name, &row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return core.ErrNotFound
		}
		p.Name, p.RPID, p.CreatedAt, p.LastUsedAt = row.Name, row.RpID, row.CreatedAt, row.LastUsedAt
		return auditSelf(ctx, tx, userID, "user.passkey.rename", map[string]any{"passkey_id": id, "name": name})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// renamePasskey renames the passkey id of userID and reads it into dst.
func renamePasskey(db *gorm.DB, id, userID int64, name string, dst *model.UserPasskey) *gorm.DB {
	return db.Model(dst).Clauses(clause.Returning{Columns: []clause.Column{{Name: "name"}, {Name: "rp_id"}, {Name: "created_at"}, {Name: "last_used_at"}}}).
		Where("id = ? AND user_id = ?", id, userID).Update("name", name)
}

// DeletePasskey removes one of the user's passkeys.
func (s *Service) DeletePasskey(ctx context.Context, userID, id int64) error {
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := s.keepLastFactor(ctx, tx, userID, core.StepUpSelfPasskeyDelete); err != nil {
			return err
		}
		res := deletePasskey(store.G(ctx, tx), id, userID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return core.ErrNotFound
		}
		return auditSelf(ctx, tx, userID, "user.passkey.delete", map[string]any{"passkey_id": id})
	})
}

// passkeyByID is the passkey id of userID.
func passkeyByID(db *gorm.DB, id, userID int64) *gorm.DB {
	return db.Model(&model.UserPasskey{}).Where("id = ? AND user_id = ?", id, userID)
}

// deletePasskey removes the passkey id of userID.
func deletePasskey(db *gorm.DB, id, userID int64) *gorm.DB {
	return passkeyByID(db, id, userID).Delete(&model.UserPasskey{})
}

// userPasskeyByCredential is the passkey of userID with credentialID.
func userPasskeyByCredential(db *gorm.DB, userID int64, credentialID []byte) *gorm.DB {
	return passkeysOf(db, userID).Where("credential_id = ?", credentialID)
}

// usePasskey stores the credential of the passkey that passkey (passkeyByID,
// userPasskeyByCredential) selects as just verified (its sign count) and
// when it was used.
func usePasskey(passkey *gorm.DB, credential []byte) *gorm.DB {
	return passkey.Updates(map[string]any{"credential": credential, "last_used_at": gorm.Expr("now()")})
}
