package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	listenAddress           = "0.0.0.0:3000"
	giteeHost               = "gitee.com"
	giteeOrigin             = "https://gitee.com"
	giteeTokenPath          = "/oauth/token"
	giteeUserPath           = "/api/v5/user"
	giteeEmailsPath         = "/api/v5/emails"
	requestTimeout          = 30 * time.Second
	connectionTimeout       = 5 * time.Second
	maximumResponseSize     = 1024 * 1024
	maximumTokenRequestSize = 64 * 1024
)

type flexibleID string

type giteeUser struct {
	ID        flexibleID `json:"id"`
	Login     string     `json:"login"`
	Name      string     `json:"name"`
	AvatarURL string     `json:"avatar_url"`
	Email     string     `json:"email"`
}

type giteeEmail struct {
	Email string   `json:"email"`
	State string   `json:"state"`
	Scope []string `json:"scope"`
}

type dexProfile struct {
	ID            string `json:"id"`
	Login         string `json:"login"`
	Name          string `json:"name"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

type giteeResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

type giteeStatusError struct {
	Endpoint   string
	StatusCode int
}

type giteeTLSDialer struct {
	resolver *net.Resolver
	dialer   *net.Dialer

	mu               sync.RWMutex
	preferredAddress string
}

var giteeClient = newGiteeClient()

func (statusError *giteeStatusError) Error() string {
	return fmt.Sprintf(
		"Gitee %s endpoint returned status %d",
		statusError.Endpoint,
		statusError.StatusCode,
	)
}

func (id *flexibleID) UnmarshalJSON(value []byte) error {
	trimmedValue := bytes.TrimSpace(value)
	if len(trimmedValue) == 0 {
		return errors.New("empty Gitee user id")
	}

	if trimmedValue[0] == '"' {
		var stringID string
		if err := json.Unmarshal(trimmedValue, &stringID); err != nil {
			return err
		}
		if stringID == "" {
			return errors.New("empty Gitee user id")
		}

		*id = flexibleID(stringID)
		return nil
	}

	numericID := string(trimmedValue)
	for _, character := range numericID {
		if character < '0' || character > '9' {
			return errors.New("invalid Gitee user id")
		}
	}

	*id = flexibleID(numericID)
	return nil
}

func newGiteeClient() *http.Client {
	tlsDialer := &giteeTLSDialer{
		resolver: net.DefaultResolver,
		dialer: &net.Dialer{
			Timeout:   connectionTimeout,
			KeepAlive: 30 * time.Second,
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialTLSContext:        tlsDialer.dialTLSContext,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
	}
}

func (dialer *giteeTLSDialer) resolveAddresses(
	ctx context.Context,
	host string,
) ([]string, error) {
	resolvedAddresses, err := dialer.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	addresses := make([]string, 0, len(resolvedAddresses))
	seen := make(map[string]struct{}, len(resolvedAddresses))
	for _, resolvedAddress := range resolvedAddresses {
		ipv4Address := resolvedAddress.IP.To4()
		if ipv4Address == nil {
			continue
		}

		address := ipv4Address.String()
		if _, exists := seen[address]; exists {
			continue
		}

		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		return nil, errors.New("Gitee DNS returned no IPv4 addresses")
	}

	dialer.mu.RLock()
	preferredAddress := dialer.preferredAddress
	dialer.mu.RUnlock()
	if _, exists := seen[preferredAddress]; !exists {
		return addresses, nil
	}

	orderedAddresses := []string{preferredAddress}
	for _, address := range addresses {
		if address != preferredAddress {
			orderedAddresses = append(orderedAddresses, address)
		}
	}

	return orderedAddresses, nil
}

func (dialer *giteeTLSDialer) dialTLSContext(
	ctx context.Context,
	_ string,
	address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host != giteeHost {
		return nil, fmt.Errorf("unexpected TLS host %q", host)
	}

	addresses, err := dialer.resolveAddresses(ctx, host)
	if err != nil {
		return nil, err
	}

	connectionErrors := make([]error, 0, len(addresses))
	for _, resolvedAddress := range addresses {
		attemptContext, cancelAttempt := context.WithTimeout(
			ctx,
			connectionTimeout,
		)
		rawConnection, dialErr := dialer.dialer.DialContext(
			attemptContext,
			"tcp",
			net.JoinHostPort(resolvedAddress, port),
		)
		cancelAttempt()
		if dialErr != nil {
			connectionErrors = append(connectionErrors, dialErr)
			continue
		}

		tlsConnection := tls.Client(rawConnection, &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: host,
		})
		handshakeContext, cancelHandshake := context.WithTimeout(
			ctx,
			connectionTimeout,
		)
		handshakeErr := tlsConnection.HandshakeContext(handshakeContext)
		cancelHandshake()
		if handshakeErr != nil {
			_ = rawConnection.Close()
			connectionErrors = append(connectionErrors, handshakeErr)
			continue
		}

		dialer.mu.Lock()
		dialer.preferredAddress = resolvedAddress
		dialer.mu.Unlock()
		return tlsConnection, nil
	}

	return nil, errors.Join(connectionErrors...)
}

func requestGitee(
	ctx context.Context,
	method string,
	path string,
	headers http.Header,
	body []byte,
) (giteeResponse, error) {
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		method,
		giteeOrigin+path,
		requestBody,
	)
	if err != nil {
		return giteeResponse{}, err
	}
	if headers != nil {
		request.Header = headers.Clone()
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "outline-gitee-auth/1.0")

	response, err := giteeClient.Do(request)
	if err != nil {
		return giteeResponse{}, err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(
		io.LimitReader(response.Body, maximumResponseSize+1),
	)
	if err != nil {
		return giteeResponse{}, err
	}
	if len(responseBody) > maximumResponseSize {
		return giteeResponse{}, errors.New(
			"Gitee API response exceeded the size limit",
		)
	}

	return giteeResponse{
		StatusCode:  response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        responseBody,
	}, nil
}

func fetchGiteeUser(ctx context.Context, accessToken string) (giteeUser, error) {
	query := url.Values{"access_token": []string{accessToken}}
	response, err := requestGitee(
		ctx,
		http.MethodGet,
		giteeUserPath+"?"+query.Encode(),
		nil,
		nil,
	)
	if err != nil {
		return giteeUser{}, err
	}
	if response.StatusCode != http.StatusOK {
		return giteeUser{}, &giteeStatusError{
			Endpoint:   "user",
			StatusCode: response.StatusCode,
		}
	}

	var user giteeUser
	if err := json.Unmarshal(response.Body, &user); err != nil {
		return giteeUser{}, err
	}

	return user, nil
}

func fetchGiteeEmails(
	ctx context.Context,
	accessToken string,
) ([]giteeEmail, error) {
	query := url.Values{"access_token": []string{accessToken}}
	response, err := requestGitee(
		ctx,
		http.MethodGet,
		giteeEmailsPath+"?"+query.Encode(),
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, &giteeStatusError{
			Endpoint:   "emails",
			StatusCode: response.StatusCode,
		}
	}

	var emails []giteeEmail
	if err := json.Unmarshal(response.Body, &emails); err != nil {
		return nil, err
	}

	return emails, nil
}

func isPrimaryEmail(email giteeEmail) bool {
	for _, scope := range email.Scope {
		if scope == "primary" {
			return true
		}
	}

	return false
}

func sendJSON(writer http.ResponseWriter, statusCode int, body interface{}) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(statusCode)
	if err := json.NewEncoder(writer).Encode(body); err != nil {
		log.Printf("failed to write JSON response: %T", err)
	}
}

func logGiteeRequestError(operation string, err error) {
	var statusError *giteeStatusError
	if errors.As(err, &statusError) {
		log.Printf(
			"Gitee %s request failed: endpoint=%s status=%d",
			operation,
			statusError.Endpoint,
			statusError.StatusCode,
		)
		return
	}

	log.Printf("Gitee %s request failed: %T", operation, err)
}

func handleHealth(writer http.ResponseWriter, _ *http.Request) {
	sendJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func handleUserInfo(writer http.ResponseWriter, request *http.Request) {
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		sendJSON(
			writer,
			http.StatusUnauthorized,
			map[string]string{"error": "missing_bearer_token"},
		)
		return
	}

	accessToken := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if accessToken == "" {
		sendJSON(
			writer,
			http.StatusUnauthorized,
			map[string]string{"error": "missing_bearer_token"},
		)
		return
	}

	user, err := fetchGiteeUser(request.Context(), accessToken)
	if err != nil {
		logGiteeRequestError("user", err)
		sendJSON(
			writer,
			http.StatusBadGateway,
			map[string]string{"error": "gitee_request_failed"},
		)
		return
	}
	emails, err := fetchGiteeEmails(request.Context(), accessToken)
	if err != nil {
		logGiteeRequestError("emails", err)
		sendJSON(
			writer,
			http.StatusBadGateway,
			map[string]string{"error": "gitee_request_failed"},
		)
		return
	}

	profileEmail := strings.TrimSpace(user.Email)
	var selectedEmail *giteeEmail
	for index := range emails {
		if profileEmail != "" && strings.EqualFold(emails[index].Email, profileEmail) {
			selectedEmail = &emails[index]
			break
		}
	}
	if selectedEmail == nil {
		for index := range emails {
			if isPrimaryEmail(emails[index]) {
				selectedEmail = &emails[index]
				break
			}
		}
	}

	email := profileEmail
	emailVerified := false
	if selectedEmail != nil {
		if selectedEmailValue := strings.TrimSpace(selectedEmail.Email); selectedEmailValue != "" {
			email = selectedEmailValue
		}
		emailVerified = strings.EqualFold(selectedEmail.State, "confirmed")
	}

	login := strings.TrimSpace(user.Login)
	userID := string(user.ID)
	if userID == "" || login == "" || email == "" {
		sendJSON(
			writer,
			http.StatusBadGateway,
			map[string]string{"error": "incomplete_gitee_profile"},
		)
		return
	}

	name := strings.TrimSpace(user.Name)
	if name == "" {
		name = login
	}
	sendJSON(writer, http.StatusOK, dexProfile{
		ID:            userID,
		Login:         login,
		Name:          name,
		AvatarURL:     strings.TrimSpace(user.AvatarURL),
		Email:         email,
		EmailVerified: emailVerified,
	})
}

func handleToken(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		sendJSON(
			writer,
			http.StatusUnsupportedMediaType,
			map[string]string{"error": "unsupported_media_type"},
		)
		return
	}

	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		maximumTokenRequestSize,
	)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maximumBytesError *http.MaxBytesError
		if errors.As(err, &maximumBytesError) {
			sendJSON(
				writer,
				http.StatusRequestEntityTooLarge,
				map[string]string{"error": "request_too_large"},
			)
			return
		}

		sendJSON(
			writer,
			http.StatusBadRequest,
			map[string]string{"error": "invalid_request_body"},
		)
		return
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	clientID, clientSecret, hasBasicAuthentication := request.BasicAuth()
	if hasBasicAuthentication {
		parameters, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			sendJSON(
				writer,
				http.StatusBadRequest,
				map[string]string{"error": "invalid_request_body"},
			)
			return
		}

		parameters.Set("client_id", clientID)
		parameters.Set("client_secret", clientSecret)
		body = []byte(parameters.Encode())
	} else if authorization := request.Header.Get("Authorization"); authorization != "" {
		headers.Set("Authorization", authorization)
	}

	response, err := requestGitee(
		request.Context(),
		http.MethodPost,
		giteeTokenPath,
		headers,
		body,
	)
	if err != nil {
		log.Printf("Gitee token request failed: %T", err)
		sendJSON(
			writer,
			http.StatusBadGateway,
			map[string]string{"error": "gitee_token_request_failed"},
		)
		return
	}

	contentType := response.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", contentType)
	writer.WriteHeader(response.StatusCode)
	if _, err := writer.Write(response.Body); err != nil {
		log.Printf("failed to write token response: %T", err)
	}
}

func runHealthcheck() error {
	healthClient := &http.Client{Timeout: 3 * time.Second}
	response, err := healthClient.Get("http://127.0.0.1:3000/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned status %d", response.StatusCode)
	}

	return nil
}

func runServer() error {
	multiplexer := http.NewServeMux()
	multiplexer.HandleFunc("GET /health", handleHealth)
	multiplexer.HandleFunc("GET /userinfo", handleUserInfo)
	multiplexer.HandleFunc("POST /token", handleToken)

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           multiplexer,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopSignals()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalContext.Done():
		shutdownContext, cancelShutdown := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancelShutdown()
		return server.Shutdown(shutdownContext)
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(); err != nil {
			log.Printf("healthcheck failed: %T", err)
			os.Exit(1)
		}
		return
	}

	if err := runServer(); err != nil {
		log.Fatalf("Gitee auth service failed: %T", err)
	}
}
