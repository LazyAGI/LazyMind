package assistantbridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

func reportedBrokerProtocol(status map[string]any, root string) string {
	version, _ := status["version"].(string)
	if status["ok"] != true || version == "" {
		return ""
	}
	if status["service"] == bridgeProduct && status["home"] == root && status["isolated"] == false {
		return version
	}
	// v8/v9 predate the service identifier. Recognize their health format
	// without tying the listener to an executable path or process.
	platform, _ := status["platform"].(string)
	executable, _ := status["executable"].(string)
	if status["service"] == nil && status["home"] == nil && status["isolated"] == nil &&
		(version == "v8" || version == "v9") && platform != "" && executable != "" {
		return version
	}
	return ""
}

func replaceBridge(ctx context.Context, address string, status map[string]any, token string, client ClientOptions, root string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// Keep the health check and lifecycle requests on the same connection
	// so a different listener taking over the port cannot receive a POST.
	var dialed atomic.Bool
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if !dialed.CompareAndSwap(false, true) {
				return nil, errors.New("Assistant Bridge connection changed during upgrade")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	httpClient := bridgeHTTPClient
	httpClient.Transport = transport
	current, err := bridgeHealth(ctx, &httpClient, address)
	if err != nil {
		return fmt.Errorf("check previous Assistant Bridge: %w", err)
	}
	if reportedBrokerProtocol(current, root) != status["version"] || current["service"] != status["service"] {
		return errors.New("Assistant Bridge changed during upgrade; retry startup")
	}

	if current["service"] == bridgeProduct {
		err = releaseForUpgrade(ctx, &httpClient, address, token, client, root)
	} else {
		err = shutdownForUpgrade(ctx, &httpClient, address)
	}
	if err != nil {
		return err
	}
	transport.CloseIdleConnections()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	dialer := net.Dialer{Timeout: 300 * time.Millisecond}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if errors.Is(err, connectionRefused) {
			return nil
		}
		if err == nil {
			_ = conn.Close()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for previous Assistant Bridge to stop listening: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func shutdownForUpgrade(ctx context.Context, client *http.Client, address string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/shutdown", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("stop previous Assistant Bridge: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("previous Assistant Bridge refused shutdown (HTTP %d)", response.StatusCode)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := decodeBridgeResponse(response.Body, &result); err != nil {
		return fmt.Errorf("read previous Assistant Bridge shutdown response: %w", err)
	}
	if !result.OK {
		return errors.New("previous Assistant Bridge did not confirm shutdown")
	}
	return nil
}

func releaseForUpgrade(ctx context.Context, httpClient *http.Client, address, token string, client ClientOptions, root string) error {
	release := func(client ClientOptions) (bool, error) {
		var result struct {
			Running *bool `json:"running"`
		}
		if err := manageBridgeHTTP(ctx, httpClient, address, token, "release", client, &result); err != nil {
			return false, err
		}
		if result.Running == nil {
			return false, errors.New("previous Assistant Bridge returned an invalid release response")
		}
		return *result.Running, nil
	}
	running, err := release(client)
	if err != nil || !running {
		return err
	}

	// Older shared brokers only shut down when their last registered client
	// releases. Use their saved registrations and the same control token.
	clients, err := readBridgeClients(root)
	if err != nil {
		return fmt.Errorf("read previous Assistant Bridge clients: %w", err)
	}
	if len(clients) == 0 {
		client = ClientOptions{ID: "upgrade", Home: root}
		if err := manageBridgeHTTP(ctx, httpClient, address, token, "acquire", client, nil); err != nil {
			return err
		}
		clients[client.ID] = client
	}
	for _, client := range clients {
		running, err := release(client)
		if err != nil || !running {
			return err
		}
	}
	return errors.New("previous Assistant Bridge still reports active clients after release")
}
