/**
 * 跳转支付的收银台小窗口（插件清单声明 checkoutWindow: "popup" 时使用）。
 *
 * 浏览器只允许在用户点击的同一轮事件里打开窗口，而下单是异步的：
 * 所以先在点击时同步打开空白小窗口，下单成功后再把它导航到收银台。
 * 打不开（被拦截）时返回 null，调用方退回整页跳转，保证仍能支付。
 *
 * 小窗口保留 opener 关系，用于到账后由本页关闭窗口、以及小窗口回到本站时通知本页。
 * 代价是收银台页面里的脚本可以把本站标签页导航到其他地址，所以只给站点管理员确认可信的收银台使用。
 */

type PopupHost = Pick<Window, "open" | "screenX" | "screenY" | "outerWidth" | "outerHeight"> & { screen?: Pick<Screen, "availWidth" | "availHeight"> };

const POPUP_NAME = "payment-checkout";
// 托管收银台通常在视口 ≥ 1024px 时才显示桌面双栏（左侧订单、右侧表单），
// 窄于这个宽度会退成手机单栏，挤在小窗口里显得局促。所以默认给到 1080 × 800，屏幕放不下时按可用区域缩小。
const POPUP_PREFERRED_WIDTH = 1080;
const POPUP_PREFERRED_HEIGHT = 800;
const POPUP_MIN_WIDTH = 420;
const POPUP_MIN_HEIGHT = 560;
const POPUP_SCREEN_MARGIN = 48;

/** 小窗口尺寸：优先桌面双栏尺寸，四周给屏幕留边。 */
export function paymentCheckoutPopupSize(screen?: Pick<Screen, "availWidth" | "availHeight">) {
    const fit = (preferred: number, available: number | undefined, min: number) => Math.round(Math.max(min, Math.min(preferred, available && available > 0 ? available - POPUP_SCREEN_MARGIN : preferred)));
    return { width: fit(POPUP_PREFERRED_WIDTH, screen?.availWidth, POPUP_MIN_WIDTH), height: fit(POPUP_PREFERRED_HEIGHT, screen?.availHeight, POPUP_MIN_HEIGHT) };
}
/** 写进小窗口自己的 sessionStorage，回到本站时据此确认是支付小窗口，而不是本站打开的其他窗口。 */
const POPUP_MARKER_KEY = "payment-checkout-popup";
export const PAYMENT_CHECKOUT_RETURNED_MESSAGE = "payment-checkout-returned";

/** 触屏设备上 popup 只会变成新标签页，体验不如整页跳转，按整页处理。 */
export function paymentCheckoutPopupSupported(matchMedia: ((query: string) => MediaQueryList) | undefined = globalThis.matchMedia?.bind(globalThis)) {
    try {
        return !matchMedia?.("(pointer: coarse)").matches;
    } catch {
        return true;
    }
}

export function openPaymentCheckoutPopup(host: PopupHost = window): Window | null {
    const { width, height } = paymentCheckoutPopupSize(host.screen);
    // 以当前浏览器窗口为中心；多显示器时 screenX 可能为负，不夹到 0，浏览器会自己把窗口限制在屏幕内。
    const left = Math.round(host.screenX + (host.outerWidth - width) / 2);
    const top = Math.round(host.screenY + (host.outerHeight - height) / 2);
    let popup: Window | null;
    try {
        popup = host.open("about:blank", POPUP_NAME, `popup=yes,width=${width},height=${height},left=${left},top=${top}`);
    } catch {
        return null;
    }
    if (!popup) return null;
    preparePopup(popup);
    return popup;
}

/** 空白窗口仍与本站同源：留下标记，并在下单完成前显示一行提示。写不进去不影响支付。 */
function preparePopup(popup: Window) {
    try {
        popup.sessionStorage.setItem(POPUP_MARKER_KEY, "1");
    } catch {
        // 存储不可用时只是回到本站后不自动关闭。
    }
    try {
        const { document } = popup;
        document.title = "正在打开支付页面";
        const text = document.createElement("p");
        text.textContent = "正在打开支付页面…";
        text.style.cssText = "margin:48px 24px;font:14px/1.6 system-ui,sans-serif;color:#666;text-align:center";
        document.body.replaceChildren(text);
    } catch {
        // 空白窗口不可写时保持空白。
    }
}

/**
 * 订单返回的收银台地址是本站相对路径（/api/payments/orders/<id>/checkout，由后端 302 到第三方收银台），
 * 按当前页解析成绝对地址；只接受 http(s)。
 */
export function resolvePaymentCheckoutUrl(url: string, base: string | undefined = globalThis.location?.href) {
    try {
        const target = new URL(url, base);
        return target.protocol === "http:" || target.protocol === "https:" ? target.href : null;
    } catch {
        return null;
    }
}

/** 把已打开的小窗口导航到收银台；窗口已关闭或地址无效时返回 false。 */
export function navigatePaymentCheckoutPopup(popup: Window | null, url: string, base?: string) {
    const target = resolvePaymentCheckoutUrl(url, base);
    if (!popup || popup.closed || !target) return false;
    try {
        popup.location.replace(target);
        popup.focus();
        return true;
    } catch {
        return false;
    }
}

/**
 * 下单完成后收银台该在哪打开：
 * - popup：小窗口已导航到收银台，本页显示确认框；
 * - dialog：点击时打开的空白小窗口在下单完成前被买家关掉，留在本页，可从确认框重新打开；
 * - page：整页模式、小窗口被拦截，或买家刚点「重新打开」新窗口就没了，整页跳转保证还能付。
 */
export function paymentCheckoutTarget({ navigated, popupClosed, keepPageWhenClosed }: { navigated: boolean; popupClosed: boolean; keepPageWhenClosed: boolean }) {
    if (navigated) return "popup" as const;
    if (popupClosed && keepPageWhenClosed) return "dialog" as const;
    return "page" as const;
}

export function closePaymentCheckoutPopup(popup: Window | null | undefined) {
    if (!popup || popup.closed) return;
    try {
        popup.close();
    } catch {
        // 窗口已被浏览器回收。
    }
}

/**
 * 买家在小窗口里点了收银台的「完成」，会经 /api/payments/return 回到本站钱包页。
 * 确认是本站打开的支付小窗口后，把订单号告诉打开它的页面再关掉自己；
 * 打开它的页面收到后展示该订单结果（即使买家已经关了确认框）。
 */
export function closeReturnedPaymentCheckoutPopup(orderId: string, current: Window = window) {
    let opener: Window;
    try {
        const candidate = current.opener as Window | null;
        if (!candidate || candidate === current || candidate.closed || candidate.location.origin !== current.location.origin) return;
        if (current.sessionStorage.getItem(POPUP_MARKER_KEY) !== "1") return;
        opener = candidate;
    } catch {
        // 跨源 opener 读不到 origin，说明不是本站打开的窗口。
        return;
    }
    try {
        opener.postMessage({ type: PAYMENT_CHECKOUT_RETURNED_MESSAGE, orderId }, current.location.origin);
    } catch {
        // 通知失败时本页的确认框仍会轮询到结果。
    }
    current.close();
}

/** 读取支付小窗口回到本站时发来的订单号；不是本站同源消息时返回 null。 */
export function paymentCheckoutReturnedOrderId(event: Pick<MessageEvent, "origin" | "data">, origin: string | undefined = globalThis.location?.origin) {
    if (!origin || event.origin !== origin) return null;
    const data = event.data as { type?: unknown; orderId?: unknown } | null;
    if (!data || data.type !== PAYMENT_CHECKOUT_RETURNED_MESSAGE || typeof data.orderId !== "string" || !/^[0-9a-f]{32}$/i.test(data.orderId)) return null;
    return data.orderId.toLowerCase();
}
