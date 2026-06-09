package lookup

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"xns/pkg/xns"
)

type Config struct {
	Indexer string
	Name    string
}

func Run(cfg Config) (any, error) {
	if cfg.Indexer == "" {
		return nil, fmt.Errorf("indexer is required")
	}
	if err := xns.ValidName(cfg.Name); err != nil {
		return nil, err
	}
	u := strings.TrimRight(cfg.Indexer, "/") + "/lookup?name=" + url.QueryEscape(cfg.Name)
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(data)))
	}
	return data, nil
}

func PrintJSON(v any) error {
	switch v := v.(type) {
	case []byte:
		fmt.Print(string(v))
		return nil
	case string:
		fmt.Print(v)
		return nil
	}
	return fmt.Errorf("unsupported lookup response %T", v)
}
