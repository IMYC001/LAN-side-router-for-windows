package subscription

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const MaxBytes = 8 << 20

type Summary struct {
	Nodes     int      `json:"nodes"`
	UDPMarked int      `json:"udpMarked"`
	Groups    int      `json:"groups"`
	Providers int      `json:"providers"`
	Names     []string `json:"names"`
	Note      string   `json:"note"`
}

func Parse(content []byte) (Summary, error) {
	if len(content) == 0 || len(content) > MaxBytes {
		return Summary{}, errors.New("订阅为空或超过 8 MiB")
	}
	var config struct {
		Proxies []struct {
			Name string `yaml:"name"`
			Type string `yaml:"type"`
			UDP  bool   `yaml:"udp"`
		} `yaml:"proxies"`
		Groups []struct {
			Name string `yaml:"name"`
		} `yaml:"proxy-groups"`
		Providers map[string]yaml.Node `yaml:"proxy-providers"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&config); err != nil {
		// Parser diagnostics can contain credentials from malformed YAML.
		return Summary{}, errors.New("不是有效的 Clash YAML，或存在重复字段")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Summary{}, errors.New("仅接受单个 YAML 配置文档")
	}
	if len(config.Proxies) == 0 && len(config.Providers) == 0 {
		return Summary{}, errors.New("订阅中没有 proxies 或 proxy-providers；暂不支持 Base64 URI 订阅")
	}
	result := Summary{
		Nodes: len(config.Proxies), Groups: len(config.Groups), Providers: len(config.Providers),
		Names: []string{},
		Note:  "可选择内嵌节点作为独立出口；原订阅中的规则、TUN、监听和脚本不会运行。UDP 标记不等于实际游戏能力。仅 provider 的订阅需先导出完整 YAML。",
	}
	seen := make(map[string]bool)
	for _, p := range config.Proxies {
		if p.Name == "" || p.Type == "" || seen[p.Name] {
			return Summary{}, fmt.Errorf("节点需要唯一名称和 type 字段")
		}
		seen[p.Name] = true
		result.Names = append(result.Names, p.Name)
		if p.UDP {
			result.UDPMarked++
		}
	}
	return result, nil
}
