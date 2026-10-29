package payment

import (
	"testing"

	"yingce/backend/internal/protocol"
)

// 前端按 descriptor 决定跳转方式，清单里的 checkoutWindow 必须原样带到宿主描述。
func TestDescriptorFromManifestCarriesCheckoutWindow(t *testing.T) {
	manifest := protocol.Manifest{Metadata: protocol.Metadata{ID: "popup-plugin", Version: "1.2.0"}}
	descriptor := DescriptorFromManifest(manifest, protocol.ManifestPaymentProvider{
		ID: "popup-pay", Label: "Popup", Icon: "brand:test", CheckoutMode: "redirect", CheckoutWindow: protocol.PaymentCheckoutWindowPopup,
	})
	if descriptor.CheckoutMode != "redirect" || descriptor.CheckoutWindow != protocol.PaymentCheckoutWindowPopup {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	legacy := DescriptorFromManifest(manifest, protocol.ManifestPaymentProvider{ID: "page-pay", Label: "Page", Icon: "brand:test", CheckoutMode: "redirect"})
	if legacy.CheckoutWindow != "" {
		t.Fatalf("legacy checkoutWindow = %q, want empty", legacy.CheckoutWindow)
	}
}
