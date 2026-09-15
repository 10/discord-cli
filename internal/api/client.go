package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	arikawa "github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/diamondburned/arikawa/v3/utils/httputil/httpdriver"
)

const BaseURL = "https://discord.com/api/v9"
const maxResponse = 16 << 20

type Client struct {
	sdk         *arikawa.Client
	base        string
	retryHeader string
}

func New(ctx context.Context, token, base string) *Client {
	if base == "" {
		base = BaseURL
	}
	h := httputil.NewClient()
	h.Retries = 1 // Arikawa counts attempts: zero retries forever, one never resends.
	h.Client = httpdriver.WrapClient(http.Client{Transport: boundedTransport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	c := &Client{base: strings.TrimRight(base, "/")}
	c.sdk = arikawa.NewCustomClient(token, h).WithContext(ctx)
	c.sdk.Session.UserAgent = "DiscordCLI (https://github.com/10/discord-cli, v1)"
	c.sdk.Client.OnResponse = append(c.sdk.Client.OnResponse, func(_ httpdriver.Request, r httpdriver.Response) error {
		c.retryHeader = ""
		if r != nil {
			c.retryHeader = r.GetHeader().Get("Retry-After")
		}
		return nil
	})
	return c
}

type limitedBody struct {
	io.Reader
	io.Closer
}
type boundedTransport struct{}

func (boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if resp != nil {
		resp.Body = limitedBody{io.LimitReader(resp.Body, maxResponse+1), resp.Body}
	}
	return resp, err
}

// Body creates a fresh stream only for a confirmed rate-limit retry.
type Body func() (io.ReadCloser, string, error)

func JSONBody(v any) Body {
	return func() (io.ReadCloser, string, error) {
		b, e := json.Marshal(v)
		return io.NopCloser(strings.NewReader(string(b))), "application/json", e
	}
}
func (c *Client) Request(method, path string, body Body, to any, creating bool) error {
	for attempt := 0; attempt < 5; attempt++ {
		if err := c.sdk.Context().Err(); err != nil {
			return &Error{Code: "cancelled", Message: "Command deadline or cancellation reached"}
		}
		var opts []httputil.RequestOption
		var stream io.ReadCloser
		if body != nil {
			var contentType string
			var err error
			stream, contentType, err = body()
			if err != nil {
				return err
			}
			opts = append(opts, httputil.WithBody(stream), httputil.WithContentType(contentType))
		}
		r, err := c.sdk.Request(method, c.base+path, opts...)
		if stream != nil {
			_ = stream.Close()
		}
		status := 0
		var data []byte
		if err != nil {
			var he *httputil.HTTPError
			if errors.As(err, &he) {
				status = he.Status
				data = he.Body
			} else {
				if creating {
					return uncertain(0, path)
				}
				if c.sdk.Context().Err() != nil {
					return &Error{Code: "cancelled", Message: "Command deadline or cancellation reached"}
				}
				return &Error{Code: "network_failure", Message: "Discord request failed; check connectivity"}
			}
		} else {
			status = r.GetStatus()
			data, err = io.ReadAll(r.GetBody())
			_ = r.GetBody().Close()
			if err != nil {
				if creating {
					return uncertain(status, path)
				}
				return &Error{Code: "network_failure", Message: "Discord response was interrupted"}
			}
		}
		if len(data) > maxResponse {
			if creating {
				return uncertain(status, path)
			}
			return Upstream()
		}
		if status == 429 || status == 202 {
			failure := HTTPFailure(status)
			var wait struct {
				RetryAfter float64 `json:"retry_after"`
			}
			_ = json.Unmarshal(data, &wait)
			if wait.RetryAfter <= 0 {
				wait.RetryAfter, _ = strconv.ParseFloat(c.retryHeader, 64)
			}
			if wait.RetryAfter <= 0 {
				wait.RetryAfter = 1
			}
			failure.RetryAfter = wait.RetryAfter
			failure.Details = map[string]any{"request": path, "complete": false}
			delay := time.Duration(wait.RetryAfter * float64(time.Second))
			if delay <= 0 {
				return failure
			}
			if deadline, ok := c.sdk.Context().Deadline(); attempt == 4 || (ok && time.Until(deadline) <= delay) {
				return failure
			}
			timer := time.NewTimer(delay)
			select {
			case <-c.sdk.Context().Done():
				timer.Stop()
				return failure
			case <-timer.C:
				continue
			}
		}
		if status < 200 || status >= 300 {
			if creating && status >= 500 {
				return uncertain(status, path)
			}
			failure := HTTPFailure(status)
			var challenge struct {
				CaptchaKey json.RawMessage `json:"captcha_key"`
				Code       int             `json:"code"`
			}
			_ = json.Unmarshal(data, &challenge)
			if len(challenge.CaptchaKey) > 0 || challenge.Code == 40002 || challenge.Code == 60003 {
				failure.Code = "user_action_required"
				failure.Message = "Complete Discord's verification or authentication challenge in the desktop app; no automatic retry was made"
			}
			return failure
		}
		if to == nil {
			return nil
		}
		if len(data) == 0 || strings.TrimSpace(string(data)) == "null" || json.Unmarshal(data, to) != nil {
			if creating {
				return uncertain(status, path)
			}
			return Upstream()
		}
		return nil
	}
	return Upstream()
}
func uncertain(status int, path string) error {
	return &Error{Code: "uncertain_write", Message: "The write may have succeeded; inspect the destination before resending", Status: status, Details: map[string]string{"request": path}}
}

type User struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	GlobalName    *string `json:"global_name,omitempty"`
	Discriminator string  `json:"discriminator,omitempty"`
}

func (c *Client) Me() (*User, error) {
	var u User
	if err := c.Request("GET", "/users/@me", nil, &u, false); err != nil {
		return nil, err
	}
	if !ID(u.ID) || u.Username == "" {
		return nil, Upstream()
	}
	return &u, nil
}
