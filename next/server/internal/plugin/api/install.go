package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
)

// consentKeys are the permissions a consent checks against the caller
// (install.checkGrantRights, checkRoleGrantRights), whether it is sent on
// its own or with the install.
var consentKeys = []perm.Key{perm.PluginGrantHigh, perm.PluginGrantCritical, perm.PluginGrantDBSchemaUnconfined, perm.RoleDefine}

// marketRequest is the body of POST /plugins/install-from-market and
// /plugins/review-from-market (which ignores consent).
type marketRequest struct {
	SourceID  int64                   `json:"source_id"` // the channel; 0 or absent: the default one
	Publisher string                  `json:"publisher"`
	Key       string                  `json:"key"`
	Version   string                  `json:"version"`
	Consent   *install.InstallConsent `json:"consent,omitempty"`
}

// upload takes a package (multipart field "file") and records it
// (install.Upload) or, with field "consent", installs it with the
// operator's consent (installPackage).
func (a *API) upload(c *gin.Context) {
	data, ok := a.packageFile(c)
	if !ok {
		return
	}
	consent, ok := formConsent(c)
	if ok && a.installStepUp(c, consent) {
		a.installPackage(c, data, install.UploadOptions{Source: "upload"}, consent)
	}
}

// installFromMarket downloads a market version and records it or, with
// consent, installs it with the operator's consent (installPackage).
func (a *API) installFromMarket(c *gin.Context) {
	in, ok := bindMarket(c)
	if !ok || !a.installStepUp(c, in.Consent) {
		return
	}
	if data, opt, ok := a.marketPackage(c, in); ok {
		a.installPackage(c, data, opt, in.Consent)
	}
}

// reviewUpload is the install preview of an uploaded package
// (install.ReviewPackage): it writes nothing.
func (a *API) reviewUpload(c *gin.Context) {
	if data, ok := a.packageFile(c); ok {
		a.reviewPackage(c, data, install.UploadOptions{Source: "upload"})
	}
}

// reviewFromMarket is the install preview of a market version, downloaded
// and checked against the index as for an install.
func (a *API) reviewFromMarket(c *gin.Context) {
	in, ok := bindMarket(c)
	if !ok {
		return
	}
	if data, opt, ok := a.marketPackage(c, in); ok {
		a.reviewPackage(c, data, opt)
	}
}

func (a *API) reviewPackage(c *gin.Context, data []byte, opt install.UploadOptions) {
	r, err := a.d.Install.ReviewPackage(ctx(c), data, actor(c), opt)
	reply(c, r, err)
}

// installPackage records data (install.Upload: the review) or, with
// consent, installs it with the operator's consent
// (install.InstallWithConsent: {review, consent}).
func (a *API) installPackage(c *gin.Context, data []byte, opt install.UploadOptions, consent *install.InstallConsent) {
	if consent == nil {
		r, err := a.d.Install.Upload(ctx(c), data, actor(c), opt)
		reply(c, r, err)
		return
	}
	res, err := a.d.Install.InstallWithConsent(ctx(c), data, actor(c), opt, *consent)
	reply(c, res, err)
}

// installStepUp spends the request's step-up proof once the handler has
// read the body (CONTRACTS §80.2): on plugin.grant when the install carries
// the consent, the consent route's scope (a request carries one proof, so
// the consent's covers the install it comes with), else on plugin.install.
func (a *API) installStepUp(c *gin.Context, consent *install.InstallConsent) bool {
	scope := core.StepUpPluginInstall
	if consent != nil {
		scope = core.StepUpPluginGrant
	}
	return a.stepUp(c, scope)
}

// packageFile reads the package in multipart field "file", within the
// package size limit.
func (a *API) packageFile(c *gin.Context) ([]byte, bool) {
	max := a.d.Install.Limits().MaxPackageBytes
	if max <= 0 {
		max = rc.PluginPackageMaxBytes
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "file", Code: "required", Message: "multipart field \"file\" is required (or too large)"}))
		return nil, false
	}
	if fh.Size > max {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(fmt.Sprintf("package exceeds %d bytes", max)))
		return nil, false
	}
	f, err := fh.Open()
	if err != nil {
		httpapi.Fail(c, err)
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		httpapi.Fail(c, err)
		return nil, false
	}
	return data, true
}

// formConsent reads the optional multipart field "consent", an
// install.InstallConsent as JSON; nil when absent.
func formConsent(c *gin.Context) (*install.InstallConsent, bool) {
	raw := c.PostForm("consent")
	if raw == "" {
		return nil, true
	}
	var in install.InstallConsent
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "consent", Code: "invalid", Message: "multipart field \"consent\" must be a JSON object: " + err.Error()}))
		return nil, false
	}
	return &in, true
}

// bindMarket reads and checks a marketRequest.
func bindMarket(c *gin.Context) (*marketRequest, bool) {
	var in marketRequest
	if !httpapi.BindJSON(c, &in) {
		return nil, false
	}
	if in.SourceID < 0 || in.Publisher == "" || in.Key == "" || in.Version == "" {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("publisher, key and version are required, source_id (the channel, default when absent) is positive"))
		return nil, false
	}
	return &in, true
}

// marketPackage downloads the version in names, checked against the
// market index (market.Download), with the options to upload it with.
func (a *API) marketPackage(c *gin.Context, in *marketRequest) ([]byte, install.UploadOptions, bool) {
	if !a.marketReady(c) {
		return nil, install.UploadOptions{}, false
	}
	data, opt, err := a.d.Market.Download(ctx(c), in.SourceID, in.Publisher, in.Key, in.Version)
	if err != nil {
		httpapi.Fail(c, err)
		return nil, install.UploadOptions{}, false
	}
	return data, opt, true
}

// reply renders v, or err when there is one.
func reply(c *gin.Context, v any, err error) {
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, v)
}
