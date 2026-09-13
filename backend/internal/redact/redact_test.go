package redact

import (
	"strings"
	"testing"
)

func TestMasksMobile(t *testing.T) {
	out := Text("联系人：张三，电话 13812345678")
	if !strings.Contains(out, "138****5678") || strings.Contains(out, "13812345678") {
		t.Fatalf("mobile not masked: %q", out)
	}
}

func TestMasksEmail(t *testing.T) {
	out := Text("邮箱 zhang.san@example.com, 备用 li@test.cn")
	if strings.Contains(out, "zhang.san@example.com") || strings.Contains(out, "li@test.cn") {
		t.Fatalf("email not masked: %q", out)
	}
	if !strings.Contains(out, "***@***") {
		t.Fatalf("email mask missing: %q", out)
	}
}

func TestMasksCreditCode(t *testing.T) {
	out := Text("统一社会信用代码 91330100MA2HXY7K4B")
	if strings.Contains(out, "91330100MA2HXY7K4B") {
		t.Fatalf("credit code not masked: %q", out)
	}
	if !strings.Contains(out, "9133**********7K4B") {
		t.Fatalf("credit code mask wrong: %q", out)
	}
}

func TestMasksIdCard(t *testing.T) {
	out := Text("身份证 330106199003154611")
	if !strings.Contains(out, "****") {
		t.Fatalf("id card not masked: %q", out)
	}
}

func TestMasksLongDigitRun(t *testing.T) {
	out := Text("对公账户 6222020202001234567")
	if !strings.Contains(out, "****") || strings.Contains(out, "62220202") {
		t.Fatalf("long digit run not masked: %q", out)
	}
}

func TestLeavesNormalTextUntouched(t *testing.T) {
	out := Text("杭州市政工程需要 C30 商品混凝土约 5000m³，供应商需二级资质，30 天内供货。")
	if strings.Contains(out, "****") || strings.Contains(out, "***@***") {
		t.Fatalf("normal text wrongly redacted: %q", out)
	}
	if !strings.Contains(out, "5000m³") {
		t.Fatalf("quantity mangled: %q", out)
	}
}
