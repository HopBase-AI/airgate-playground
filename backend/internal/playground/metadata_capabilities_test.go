package playground

import (
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// 插件调用的每个 Host 方法都必须在清单里声明能力，否则 core 拒为 PermissionDenied：
// 这里把代码里用到的方法名与清单逐一对上，漏声明直接红。
func TestManifestDeclaresEveryHostMethodUsed(t *testing.T) {
	used := []string{
		hostMethodGatewayForward, hostMethodUsersGet, hostMethodModelsList, hostMethodGroupsList,
		hostMethodAssetsStore, hostMethodAssetsGetURL, hostMethodAssetsGetBytes, hostMethodAssetsDelete, hostMethodUsageRecord,
	}
	declared := map[sdk.Capability]bool{}
	for _, c := range BuildPluginInfo().Capabilities {
		declared[c] = true
	}
	for _, m := range used {
		if !declared[sdk.CapabilityForHostMethod(m)] {
			t.Fatalf("host method %q is used but not declared in manifest capabilities", m)
		}
	}
}
