// Package mailer is the "email" notification channel (CONTRACTS §74.4): the
// core hands it notifications and it sends them over SMTP.
package mailer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/egress"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// Channel is the notification channel the plugin declares.
const Channel = "email"

// maxTimeoutSeconds stays under the host's 30 s deadline of a delivery.
const maxTimeoutSeconds = 25

// Security modes of the SMTP connection.
const (
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"
	SecurityNone     = "none"
)

// Settings are the plugin settings (settingsRules).
type Settings struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Security       string `json:"security"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	FromAddress    string `json:"from_address"`
	FromName       string `json:"from_name"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// Plugin implements pluginsdk.NotificationChannel, pluginsdk.Configurer and
// pluginsdk.SettingsValidator.
type Plugin struct {
	cfg atomic.Pointer[Settings]
	// dial opens the TCP connection and lookup resolves a host name (through
	// the tunnel once it is installed); tests replace them.
	dial   func(ctx context.Context, address string) (net.Conn, error)
	lookup func(ctx context.Context, host string) ([]string, error)
}

func New() *Plugin { return &Plugin{dial: dialEgress, lookup: lookupHost} }

// lookupHost reads net.DefaultResolver at call time: egress.Install replaces
// it after New.
func lookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// dialEgress dials through the host tunnel when it is installed (strict
// network mode), directly otherwise.
func dialEgress(ctx context.Context, address string) (net.Conn, error) {
	conn, err := egress.DialContext(ctx, "tcp", address)
	if errors.Is(err, egress.ErrNotInstalled) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", address)
	}
	return conn, err
}

// Configure implements pluginsdk.Configurer.
func (p *Plugin) Configure(_ context.Context, cfg pluginsdk.Config) error {
	s, err := parseSettings(cfg)
	if err != nil {
		return err
	}
	p.cfg.Store(s)
	return nil
}

// ValidateSettings implements pluginsdk.SettingsValidator: the console's
// settings are refused before they are stored when Configure would refuse
// them.
func (p *Plugin) ValidateSettings(_ context.Context, cfg pluginsdk.Config) (json.RawMessage, error) {
	_, err := parseSettings(cfg)
	return nil, err
}

// settingsRules are the settings the console form edits (ui/native
// MailerSettings shows the same bounds); every other key is refused.
var settingsRules = fieldcheck.Fields{
	"host":            fieldcheck.String().MaxLen(253),
	"port":            fieldcheck.Integer().Range(1, 65535),
	"security":        fieldcheck.String().OneOf(SecuritySTARTTLS, SecurityTLS, SecurityNone),
	"username":        fieldcheck.String().MaxLen(320),
	"password":        fieldcheck.String().MaxLen(1000),
	"from_address":    fieldcheck.String().MaxLen(320),
	"from_name":       fieldcheck.String().MaxLen(100),
	"timeout_seconds": fieldcheck.Integer().Range(5, maxTimeoutSeconds),
}

// parseSettings decodes and checks the settings, absent keys taking their
// defaults: the one check of Configure and ValidateSettings.
func parseSettings(cfg pluginsdk.Config) (*Settings, error) {
	s := Settings{Port: 587, Security: SecuritySTARTTLS, FromName: "Sub2API", TimeoutSeconds: 20}
	if err := fieldcheck.DecodeSettings(cfg, settingsRules, &s); err != nil {
		return nil, err
	}
	s.Host, s.FromAddress = strings.TrimSpace(s.Host), strings.TrimSpace(s.FromAddress)
	if s.Host != "" && !manifest.ValidRecipient(manifest.RecipientEmail, s.FromAddress) {
		return nil, pluginsdk.FieldErrors{}.Add("from_address", "invalid", "a plain address such as noreply@example.com / 请填写发件地址，例如 noreply@example.com").Err()
	}
	return &s, nil
}

// DeliverNotification implements pluginsdk.NotificationChannel. A refusal
// of the message by the server (a 5xx reply to RCPT or DATA) is final;
// anything else, a refused login or sender included, is a setup problem the
// core retries until it is fixed.
func (p *Plugin) DeliverNotification(ctx context.Context, in *pluginv1.DeliverNotificationRequest) (*pluginv1.DeliverNotificationResponse, error) {
	s := p.cfg.Load()
	if s == nil || s.Host == "" {
		return nil, errors.New("SMTP is not configured")
	}
	if in.GetChannel() != Channel {
		return &pluginv1.DeliverNotificationResponse{Rejected: "unknown channel " + in.GetChannel()}, nil
	}
	// One bad recipient refuses the whole message: the core validates them,
	// this is the plugin's own boundary.
	for _, r := range in.GetRecipients() {
		if !manifest.ValidRecipient(manifest.RecipientEmail, r) {
			return &pluginv1.DeliverNotificationResponse{Rejected: "invalid recipient " + r}, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	msg := compose(s, in, time.Now())
	err := p.send(ctx, s, in.GetRecipients(), msg)
	var refused messageRefused
	if errors.As(err, &refused) {
		return &pluginv1.DeliverNotificationResponse{Rejected: fmt.Sprintf("SMTP %d %s", refused.reply.Code, refused.reply.Msg)}, nil
	}
	if err != nil {
		return nil, err
	}
	return &pluginv1.DeliverNotificationResponse{}, nil
}

// send runs one SMTP session. The errors of the setup steps say which step
// failed and what to check, in English and Chinese, ahead of the underlying
// error: the core shows them in the delivery record.
func (p *Plugin) send(ctx context.Context, s *Settings, to []string, msg []byte) error {
	address := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	conn, err := p.dial(ctx, address)
	if err != nil {
		return p.connectError(ctx, s.Host, address, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.Security == SecurityTLS {
		conn = tls.Client(conn, tlsConfig)
	}
	// The greeting is the first read, so it also runs the TLS handshake.
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("no SMTP greeting from %s / SMTP 服务器 %s 没有正常应答%s: %w", address, address, portHint(s), err)
	}
	defer c.Close()
	if s.Security == SecuritySTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server does not offer STARTTLS / SMTP 服务器不支持 STARTTLS")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS failed / STARTTLS 失败: %w", err)
		}
	}
	if s.Username != "" {
		// PlainAuth refuses to send the password without TLS (except to
		// localhost).
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("SMTP login failed, check the username and password / SMTP 登录失败，请检查用户名和密码: %w", err)
		}
	}
	if err := c.Mail(s.FromAddress); err != nil {
		return fmt.Errorf("the SMTP server refused the from address %s / SMTP 服务器拒绝了发件地址 %s: %w", s.FromAddress, s.FromAddress, err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return refusal(err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return refusal(err)
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return refusal(err)
	}
	// The server accepted the message: a failed QUIT must not make the core
	// send it again.
	_ = c.Quit()
	return nil
}

// connectError explains a failed connection. A host name that does not exist
// (the usual cause: a mistyped SMTP host) is told apart by looking it up
// again, because the egress tunnel reports the failure as text only.
func (p *Plugin) connectError(ctx context.Context, host, address string, err error) error {
	var dnsErr *net.DNSError
	if _, lerr := p.lookup(ctx, host); errors.As(lerr, &dnsErr) && dnsErr.IsNotFound {
		return fmt.Errorf("SMTP host %s does not exist, correct it in the mailer settings / 找不到 SMTP 服务器 %s（域名不存在），请在邮件插件设置里改正: %w", host, host, err)
	}
	return fmt.Errorf("cannot connect to SMTP server %s / 连不上 SMTP 服务器 %s: %w", address, address, err)
}

// portHint names the security mode a well-known port expects when the
// settings choose another, the usual cause of a server that does not answer.
func portHint(s *Settings) string {
	switch {
	case s.Port == 465 && s.Security != SecurityTLS:
		return "; port 465 expects TLS from the start / 端口 465 一般选“全程 TLS”"
	case s.Port == 587 && s.Security == SecurityTLS:
		return "; port 587 expects STARTTLS / 端口 587 一般选 STARTTLS"
	}
	return ""
}

// messageRefused is a 5xx reply to the message itself (its recipients or
// content), which retrying cannot change.
type messageRefused struct{ reply *textproto.Error }

func (e messageRefused) Error() string { return e.reply.Error() }

// refusal marks a 5xx reply of a message step as final.
func refusal(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 500 {
		return messageRefused{reply}
	}
	return err
}
