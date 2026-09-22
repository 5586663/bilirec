package recording

import (
	"os"

	"github.com/bilirec/bilirec/pkg/logger"
)

func init() {
	if os.Getenv("CI") != "" {
		os.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
		os.Setenv("SKIP_SMALL_FLUSH", "false")
		logger.SetLevel(logger.DebugLevel)
	}
}
