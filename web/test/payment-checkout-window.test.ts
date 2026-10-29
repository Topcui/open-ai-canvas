import { expect, mock, test } from "bun:test";

import {
    closePaymentCheckoutPopup,
    closeReturnedPaymentCheckoutPopup,
    navigatePaymentCheckoutPopup,
    openPaymentCheckoutPopup,
    PAYMENT_CHECKOUT_RETURNED_MESSAGE,
    paymentCheckoutPopupSize,
    paymentCheckoutPopupSupported,
    paymentCheckoutReturnedOrderId,
    paymentCheckoutTarget,
    resolvePaymentCheckoutUrl,
} from "../src/components/payment/payment-checkout-window";

const site = "https://example.com";
const orderId = "0123456789abcdef0123456789abcdef";
// 后端给前端的跳转地址是本站相对路径，由 /checkout 接口 302 到第三方收银台。
const checkoutPath = `/api/payments/orders/${orderId}/checkout`;

type FakePopup = Window & { close: ReturnType<typeof mock>; focus: ReturnType<typeof mock>; location: { replace: ReturnType<typeof mock> }; sessionStorage: Storage };

function memoryStorage(): Storage {
    const values = new Map<string, string>();
    return {
        get length() {
            return values.size;
        },
        clear: () => values.clear(),
        getItem: (key: string) => values.get(key) ?? null,
        key: (index: number) => [...values.keys()][index] ?? null,
        removeItem: (key: string) => void values.delete(key),
        setItem: (key: string, value: string) => void values.set(key, value),
    };
}

function fakePopup(overrides: Partial<Record<string, unknown>> = {}) {
    return {
        closed: false,
        close: mock(() => undefined),
        focus: mock(() => undefined),
        location: { replace: mock((_url: string) => undefined) },
        sessionStorage: memoryStorage(),
        ...overrides,
    } as unknown as FakePopup;
}

test("payment checkout popup opens a centered desktop-sized blank window and marks it as the payment popup", () => {
    const popup = fakePopup();
    const open = mock(() => popup);
    const host = { open, screenX: 100, screenY: 50, outerWidth: 1440, outerHeight: 900, screen: { availWidth: 1920, availHeight: 1055 } } as unknown as Window;
    expect(openPaymentCheckoutPopup(host)).toBe(popup);
    const [url, name, features] = open.mock.calls[0] as unknown as [string, string, string];
    expect(url).toBe("about:blank");
    expect(name).toBe("payment-checkout");
    expect(features).toBe("popup=yes,width=1080,height=800,left=280,top=100");
    expect(popup.sessionStorage.getItem("payment-checkout-popup")).toBe("1");
});

test("payment checkout popup size prefers the desktop two-column width and shrinks to small screens", () => {
    expect(paymentCheckoutPopupSize({ availWidth: 1920, availHeight: 1055 })).toEqual({ width: 1080, height: 800 });
    // 1280 × 800 的笔记本：四周留 48px。
    expect(paymentCheckoutPopupSize({ availWidth: 1280, availHeight: 775 })).toEqual({ width: 1080, height: 727 });
    expect(paymentCheckoutPopupSize({ availWidth: 1024, availHeight: 700 })).toEqual({ width: 976, height: 652 });
    expect(paymentCheckoutPopupSize({ availWidth: 300, availHeight: 400 })).toEqual({ width: 420, height: 560 });
    expect(paymentCheckoutPopupSize(undefined)).toEqual({ width: 1080, height: 800 });
});

test("payment checkout popup keeps negative screen coordinates for monitors left of the primary screen", () => {
    const open = mock(() => fakePopup());
    openPaymentCheckoutPopup({ open, screenX: -1920, screenY: 0, outerWidth: 1280, outerHeight: 900, screen: { availWidth: 1920, availHeight: 1055 } } as unknown as Window);
    expect((open.mock.calls[0] as unknown as [string, string, string])[2]).toContain("left=-1820");
});

test("payment checkout popup reports blocked windows so the caller can fall back to a full-page redirect", () => {
    const blocked = { open: mock(() => null), screenX: 0, screenY: 0, outerWidth: 0, outerHeight: 0 } as unknown as Window;
    expect(openPaymentCheckoutPopup(blocked)).toBeNull();
    const throwing = {
        open: mock(() => {
            throw new Error("blocked");
        }),
        screenX: 0,
        screenY: 0,
        outerWidth: 0,
        outerHeight: 0,
    } as unknown as Window;
    expect(openPaymentCheckoutPopup(throwing)).toBeNull();
});

test("payment checkout URLs resolve the backend's relative redirect path and reject non-http schemes", () => {
    expect(resolvePaymentCheckoutUrl(checkoutPath, `${site}/canvas/1`)).toBe(`${site}${checkoutPath}`);
    expect(resolvePaymentCheckoutUrl("https://checkout.example.net/store/demo/checkout/cs_1", site)).toBe("https://checkout.example.net/store/demo/checkout/cs_1");
    expect(resolvePaymentCheckoutUrl("javascript:alert(1)", site)).toBeNull();
    expect(resolvePaymentCheckoutUrl("data:text/html,hi", site)).toBeNull();
});

test("payment checkout popup navigates open windows to the resolved checkout URL", () => {
    const popup = fakePopup();
    expect(navigatePaymentCheckoutPopup(popup, checkoutPath, site)).toBe(true);
    expect(popup.location.replace).toHaveBeenCalledWith(`${site}${checkoutPath}`);
    expect(popup.focus).toHaveBeenCalled();

    expect(navigatePaymentCheckoutPopup(fakePopup(), "javascript:alert(1)", site)).toBe(false);
    expect(navigatePaymentCheckoutPopup(fakePopup({ closed: true }), checkoutPath, site)).toBe(false);
    expect(navigatePaymentCheckoutPopup(null, checkoutPath, site)).toBe(false);
});

test("payment checkout target keeps the buyer able to pay in every branch", () => {
    expect(paymentCheckoutTarget({ navigated: true, popupClosed: false, keepPageWhenClosed: true })).toBe("popup");
    // 下单前买家自己关了空白窗口：留在本页，确认框里可重新打开。
    expect(paymentCheckoutTarget({ navigated: false, popupClosed: true, keepPageWhenClosed: true })).toBe("dialog");
    // 「重新打开」后新窗口立刻没了：多半被拦截，整页跳转。
    expect(paymentCheckoutTarget({ navigated: false, popupClosed: true, keepPageWhenClosed: false })).toBe("page");
    // 小窗口被拦截（null）或整页模式。
    expect(paymentCheckoutTarget({ navigated: false, popupClosed: false, keepPageWhenClosed: true })).toBe("page");
});

test("payment checkout popup is skipped on touch devices", () => {
    const query = (coarse: boolean) => mock(() => ({ matches: coarse }) as MediaQueryList);
    expect(paymentCheckoutPopupSupported(query(false))).toBe(true);
    expect(paymentCheckoutPopupSupported(query(true))).toBe(false);
    expect(paymentCheckoutPopupSupported(undefined)).toBe(true);
});

test("payment checkout popup close ignores missing or already closed windows", () => {
    const popup = fakePopup();
    closePaymentCheckoutPopup(popup);
    expect(popup.close).toHaveBeenCalledTimes(1);
    const closed = fakePopup({ closed: true });
    closePaymentCheckoutPopup(closed);
    expect(closed.close).not.toHaveBeenCalled();
    closePaymentCheckoutPopup(null);
});

function returnedWindow(opener: unknown, marked: boolean) {
    const sessionStorage = memoryStorage();
    if (marked) sessionStorage.setItem("payment-checkout-popup", "1");
    return { opener, location: { origin: site }, sessionStorage, close: mock(() => undefined) } as unknown as Window & { close: ReturnType<typeof mock> };
}

test("returned checkout popup notifies its opener and closes only when it is this site's payment popup", () => {
    const postMessage = mock((_message: unknown, _origin: string) => undefined);
    const opener = { closed: false, location: { origin: site }, postMessage };
    const popup = returnedWindow(opener, true);
    closeReturnedPaymentCheckoutPopup(orderId, popup);
    expect(postMessage).toHaveBeenCalledWith({ type: PAYMENT_CHECKOUT_RETURNED_MESSAGE, orderId }, site);
    expect(popup.close).toHaveBeenCalledTimes(1);

    // 本站打开的其他窗口（没有支付小窗口标记）不关闭。
    const unmarked = returnedWindow({ closed: false, location: { origin: site }, postMessage: mock(() => undefined) }, false);
    closeReturnedPaymentCheckoutPopup(orderId, unmarked);
    expect(unmarked.close).not.toHaveBeenCalled();

    const noOpener = returnedWindow(null, true);
    closeReturnedPaymentCheckoutPopup(orderId, noOpener);
    expect(noOpener.close).not.toHaveBeenCalled();

    const crossOrigin = returnedWindow(
        {
            closed: false,
            get location(): Location {
                throw new DOMException("Blocked a frame with origin", "SecurityError");
            },
        },
        true,
    );
    closeReturnedPaymentCheckoutPopup(orderId, crossOrigin);
    expect(crossOrigin.close).not.toHaveBeenCalled();
});

test("returned checkout messages are accepted only from this site with a valid order id", () => {
    const message = { type: PAYMENT_CHECKOUT_RETURNED_MESSAGE, orderId };
    expect(paymentCheckoutReturnedOrderId({ origin: site, data: message }, site)).toBe(orderId);
    expect(paymentCheckoutReturnedOrderId({ origin: "https://checkout.example.net", data: message }, site)).toBeNull();
    expect(paymentCheckoutReturnedOrderId({ origin: site, data: { ...message, orderId: "../admin" } }, site)).toBeNull();
    expect(paymentCheckoutReturnedOrderId({ origin: site, data: { type: "other", orderId } }, site)).toBeNull();
    expect(paymentCheckoutReturnedOrderId({ origin: site, data: null }, site)).toBeNull();
});
