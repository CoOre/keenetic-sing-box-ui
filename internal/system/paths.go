package system

import "path/filepath"

type Paths struct {
	Opt           string `json:"opt"`
	Opkg          string `json:"opkg"`
	SingBoxBin    string `json:"sing_box_bin"`
	SingBoxConfig string `json:"sing_box_config"`
	SingBoxLog    string `json:"sing_box_log"`
	SingBoxCache  string `json:"sing_box_cache"`
	SingBoxInit   string `json:"sing_box_init"`
	UIConfigDir   string `json:"ui_config_dir"`
	UITLSDir      string `json:"ui_tls_dir"`
}

func DefaultPaths() Paths {
	return PathsRooted("/")
}

func PathsRooted(root string) Paths {
	opt := filepath.Join(root, "opt")
	return Paths{
		Opt:           opt,
		Opkg:          filepath.Join(opt, "bin", "opkg"),
		SingBoxBin:    filepath.Join(opt, "bin", "sing-box"),
		SingBoxConfig: filepath.Join(opt, "etc", "sing-box", "config.json"),
		SingBoxLog:    filepath.Join(opt, "var", "log", "sing-box.log"),
		SingBoxCache:  filepath.Join(opt, "var", "lib", "sing-box", "cache.db"),
		SingBoxInit:   filepath.Join(opt, "etc", "init.d", "S99sing-box"),
		UIConfigDir:   filepath.Join(opt, "etc", "keenetic-sing-box-ui"),
		UITLSDir:      filepath.Join(opt, "etc", "keenetic-sing-box-ui", "tls"),
	}
}
