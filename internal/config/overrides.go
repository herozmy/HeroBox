package config

// Overrides 表示 mosdns config_overrides.json 的结构，支持在 herobox.yaml 中持久化。
type Overrides struct {
	Socks5       string                `yaml:"socks5,omitempty" json:"socks5,omitempty"`
	ECS          string                `yaml:"ecs,omitempty" json:"ecs,omitempty"`
	Replacements []OverrideReplacement `yaml:"replacements,omitempty" json:"replacements,omitempty"`
}

// OverrideReplacement 定义单个替换项。
type OverrideReplacement struct {
	Original string `yaml:"original" json:"original"`
	New      string `yaml:"new" json:"new"`
	Comment  string `yaml:"comment,omitempty" json:"comment,omitempty"`
}

// Clone 返回一个深拷贝，避免外部调用方修改内部切片。
func (o Overrides) Clone() Overrides {
	clone := o
	if len(o.Replacements) > 0 {
		clone.Replacements = append([]OverrideReplacement(nil), o.Replacements...)
	}
	return clone
}
