package interceptors

// import (
// 	"net"
// 	"net/http"
// 	"strings"
// 	"time"

// 	"github.com/everstacklabs/everstack/internal/api/grpc/server/middleware"
// 	http_utils "github.com/everstacklabs/everstack/internal/api/http"
// 	"github.com/everstacklabs/everstack/internal/lib/logger"
// 	"github.com/everstacklabs/everstack/internal/logstore"
// 	"github.com/everstacklabs/everstack/internal/logstore/record"
// )

// type AccessConfig struct {
// 	ExhaustedCookieKey    string
// 	ExhaustedCookieMaxAge time.Duration
// }

// type AccessInterceptor struct {
// 	logstoreSvc   *logstore.Service[*record.AccessLog]
// 	cookieHandler *http_utils.CookieHandler
// 	limitConfig   *AccessConfig
// 	storeOnly     bool
// 	redirect      string
// }

// func NewAccessInterceptor(svc *logstore.Service[*record.AccessLog], cookieHandler *http_utils.CookieHandler, cookieConfig *AccessConfig) *AccessInterceptor {
// 	return &AccessInterceptor{
// 		logstoreSvc:   svc,
// 		cookieHandler: cookieHandler,
// 		limitConfig:   cookieConfig,
// 	}
// }

// func (a *AccessInterceptor) WithoutLimiting() *AccessInterceptor {
// 	return &AccessInterceptor{
// 		logstoreSvc:   a.logstoreSvc,
// 		cookieHandler: a.cookieHandler,
// 		limitConfig:   a.limitConfig,
// 		storeOnly:     true,
// 		redirect:      a.redirect,
// 	}
// }

// func (a *AccessInterceptor) AccessService() *logstore.Service[*record.AccessLog] {
// 	return a.logstoreSvc
// }

// func (a *AccessInterceptor) Limit(w http.ResponseWriter, r *http.Request, publicAuthPathPrefixes ...string) bool {
// 	if a.storeOnly {
// 		return false
// 	}
// 	ctx := r.Context()

// TODO: Need to create a middleware that will set the instance in the context and then we can get the instance from the context. We do have a file /internal/api/auth/instance.go

// instance := authz.GetInstance(ctx)
// var deleteCookie bool
// defer func() {
// 	if deleteCookie {
// 		a.DeleteExhaustedCookie(w)
// 	}
// }()
// if block := instance.Block(); block != nil {
// 	if *block {
// 		a.SetExhaustedCookie(w, r)
// 		return true
// 	}
// 	deleteCookie = true
// }
// for _, ignoredPathPrefix := range publicAuthPathPrefixes {
// 	if strings.HasPrefix(r.RequestURI, ignoredPathPrefix) {
// 		return false
// 	}
// }
// remaining := a.logstoreSvc.Limit(ctx, instance.InstanceID())
// if remaining != nil {
// 	if remaining != nil && *remaining > 0 {
// 		deleteCookie = true
// 		return false
// 	}
// 	a.SetExhaustedCookie(w, r)
// 	return true
// }
// 	return false
// }

// func (a *AccessInterceptor) SetExhaustedCookie(writer http.ResponseWriter, request *http.Request) {
// 	cookieValue := "true"
// 	host := request.Header.Get(middleware.HTTP1Host)
// 	domain := host
// 	if strings.ContainsAny(host, ":") {
// 		var err error
// 		domain, _, err = net.SplitHostPort(host)
// 		if err != nil {
// 			logger.WithError(err).WithField("host", host).Warning("failed to extract cookie domain from request host")
// 		}
// 	}
// 	a.cookieHandler.SetCookie(writer, a.limitConfig.ExhaustedCookieKey, domain, cookieValue)
// }

// func (a *AccessInterceptor) DeleteExhaustedCookie(writer http.ResponseWriter) {
// 	a.cookieHandler.DeleteCookie(writer, a.limitConfig.ExhaustedCookieKey)
// }

// // func (a *AccessInterceptor) HandleWithPublicAuthPathPrefixes(publicPathPrefixes []string) func(next http.Handler) http.Handler {
// // 	return a.handle(publicPathPrefixes...)
// // }

// // func (a *AccessInterceptor) Handle(next http.Handler) http.Handler {
// // 	return a.handle()(next)
// // }
