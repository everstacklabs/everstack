package idgenerator

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/drone/envsubst"
	"github.com/jarcoal/jpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/sony/sonyflake"
)

type sonyflakeGenerator struct {
	generator *sonyflake.Sonyflake
}

func (s *sonyflakeGenerator) Next() (string, error) {
	id, err := s.generator.NextID()
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(id, 10), nil
}

var (
	GeneratorConfig *Config
	sonyflakeGen    *sonyflakeGenerator
)

func SonyFlakeGen() Generator {
	if sonyflakeGen == nil {
		sonyflakeGen = &sonyflakeGenerator{
			generator: sonyflake.NewSonyflake(sonyflake.Settings{}),
		}
	}
	return sonyflakeGen
}

func privateIPv4() (net.IP, error) {
	as, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, a := range as {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}

		ip := ipnet.IP.To4()
		if isPrivateIPv4(ip) {
			return ip, nil
		}
	}

	//change: use "POD_IP"
	ip := net.ParseIP(os.Getenv("POD_IP"))
	if ip == nil {
		return nil, errors.New("no private ip address")
	}
	if ipV4 := ip.To4(); ipV4 != nil {
		return ipV4, nil
	}
	return nil, errors.New("no pod ipv4 address")
}

func isPrivateIPv4(ip net.IP) bool {
	return ip != nil &&
		(ip[0] == 10 || ip[0] == 172 && (ip[1] >= 16 && ip[1] < 32) || ip[0] == 192 && ip[1] == 168)
}

func MachineIdentificationMethod() string {
	if GeneratorConfig.Identification.PrivateIp.Enabled {
		return "Private IP"
	}

	if GeneratorConfig.Identification.Hostname.Enabled {
		return "Hostname"
	}

	if GeneratorConfig.Identification.Webhook.Enabled {
		return "webhook"
	}

	return "No Machine Identification Method Enabled"
}

func MachineID() (uint16, error) {
	if GeneratorConfig == nil {
		logger.Panic("cannot create a unique id for the machine, generator has not been configured")
	}

	errors := []string{}

	if GeneratorConfig.Identification.PrivateIp.Enabled {
		ip, err := lower16BitPrivateIP()

		if err == nil {
			return ip, nil
		}

		errors = append(errors, fmt.Sprintf("unable to get private ip: %s", err))
	}

	if GeneratorConfig.Identification.Hostname.Enabled {
		host, err := hostname()
		if err == nil {
			return host, nil
		}
		errors = append(errors, fmt.Sprintf("unable to get hostname: %s", err))
	}

	if GeneratorConfig.Identification.Webhook.Enabled {
		webhookID, err := metadataWebhookID()
		if err == nil {
			return webhookID, nil
		}
		errors = append(errors, fmt.Sprintf("failed to query metadata webhook id: %s", err))
	}

	if len(errors) == 0 {
		errors = append(errors, "no machine identification method enabled")
	}

	logger.WithFields("errors", strings.Join(errors, ",")).Panic("none of the enabled methods for identifying the machine were successful")

	return 0, nil
}

func lower16BitPrivateIP() (uint16, error) {
	ip, err := privateIPv4()
	if err != nil {
		return 0, err
	}

	return uint16(ip[2])<<8 + uint16(ip[3]), nil
}

func hostname() (uint16, error) {
	host, err := os.Hostname()
	if err != nil {
		return 0, err
	}

	h := fnv.New32()
	_, hashErr := h.Write([]byte(host))
	if hashErr != nil {
		return 0, hashErr
	}

	return uint16(h.Sum32()), nil
}

func metadataWebhookID() (uint16, error) {
	webhook := GeneratorConfig.Identification.Webhook

	url, err := envsubst.EvalEnv(webhook.Url)
	if err != nil {
		url = webhook.Url
	}

	req, err := http.NewRequest(
		http.MethodGet,
		url,
		nil,
	)

	if err != nil {
		return 0, err
	}

	if webhook.Headers != nil {
		for k, v := range *webhook.Headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode < 600 {
		return 0, fmt.Errorf("metadata endpoint returned an unsuccessful status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	data, err := extractMetadataResponse(webhook.JSONPath, body)
	if err != nil {
		return 0, err
	}

	h := fnv.New32()
	if _, err := h.Write([]byte(data)); err != nil {
		return 0, err
	}

	return uint16(h.Sum32()), nil
}

func extractMetadataResponse(path *string, data []byte) ([]byte, error) {
	if path != nil {
		jp, err := jpath.NewFromBytes(data)
		if err != nil {
			return nil, err
		}

		results := jp.Query(*path)
		if len(results) == 0 {
			return nil, fmt.Errorf("no results found for path %s", *path)
		}

		return json.Marshal(results)
	}
	return data, nil
}
