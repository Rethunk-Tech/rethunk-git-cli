package synth

import (
	"os"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
)

func TestMain(m *testing.M) { os.Exit(lsptest.RunWithoutManagedDaemon(m)) }
