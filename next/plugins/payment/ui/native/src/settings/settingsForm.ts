import type { PluginSettingsForm, Rules } from '@sub2api/ui'
import type { GatewayKey, PaymentMethod, PaymentSettingsValue } from '../host'
import { brandColor, isLogoType, LOGO_TYPES, parseLogo } from '../components/methodIcon'

// The admin payment settings as a form (usePluginSettings over
// PaymentSettingsValue): defaults, rules, tabs, and the catalogs the editors
// pick from. The plugin's ValidateSettings is the final check.

export type SettingsModel = PaymentSettingsValue & Record<string, unknown>
export type SettingsForm = PluginSettingsForm<SettingsModel>
type Translate = (key: string, params?: Record<string, unknown>) => string

export type SettingsTab = 'general' | 'methods' | 'gateways'
export const SETTINGS_TABS: SettingsTab[] = ['general', 'methods', 'gateways']

/** Top-level keys outside the general tab. */
const TAB_OF_KEY: Record<string, SettingsTab> = { methods: 'methods', epay: 'gateways', stripe: 'gateways' }

/** The tab showing a field path ('epay.key' -> gateways). */
export const tabOfPath = (path: string): SettingsTab => TAB_OF_KEY[path.split(/[.[]/)[0]] ?? 'general'

export const SETTINGS_DEFAULTS: Partial<SettingsModel> = {
  enabled: false,
  currency: 'CNY',
  price: 7.3,
  min_topup: 1,
  max_topup: 10000,
  order_timeout_minutes: 30,
  max_pending_orders: 3,
  amount_options: [],
  discounts: [],
  methods: [],
  epay: { enabled: false, endpoint: '', merchant_id: '', key: '' },
  stripe: { enabled: false, secret_key: '', webhook_secret: '', allow_promotion_codes: false }
}

/** Currencies users pay in. */
export const CURRENCIES: readonly string[] = ['CNY', 'USD', 'HKD', 'EUR', 'GBP', 'JPY']

export const GATEWAYS: GatewayKey[] = ['epay', 'stripe']

/** Type keys each gateway knows; epay also takes custom ones (its `type` parameter). */
export const GATEWAY_TYPES: Record<GatewayKey, string[]> = { epay: ['alipay', 'wxpay', 'qqpay'], stripe: ['stripe'] }

const KNOWN_TYPES = Object.values(GATEWAY_TYPES).flat()
/** A type key with a translated name (settings.methods.typeName.*). */
export const isKnownType = (type: string) => KNOWN_TYPES.includes(type)

/** The webhook a gateway calls back (the plugin's webhook route). */
export const webhookUrl = (pluginKey: string, gateway: GatewayKey) => `${window.location.origin}/api/v1/p/${pluginKey}/webhook/${gateway}`

/** Stripe events the webhook endpoint must subscribe to. */
export const STRIPE_EVENTS = ['checkout.session.completed', 'checkout.session.expired']

export type TemplateKey = 'epay_alipay' | 'epay_wxpay' | 'stripe' | 'epay_custom'
export const TEMPLATE_KEYS: TemplateKey[] = ['epay_alipay', 'epay_wxpay', 'stripe', 'epay_custom']

/** Gateway and type of each ready-made template; the custom one is left for the dialog. */
const TEMPLATES: Record<TemplateKey, Pick<PaymentMethod, 'gateway' | 'type'>> = {
  epay_alipay: { gateway: 'epay', type: 'alipay' },
  epay_wxpay: { gateway: 'epay', type: 'wxpay' },
  stripe: { gateway: 'stripe', type: 'stripe' },
  epay_custom: { gateway: 'epay', type: '' }
}

/** The type key a template adds ('' for the custom one). */
export const templateType = (key: TemplateKey) => TEMPLATES[key].type

/** A method made from a template (or a blank one for the gateway), named in the current locale. */
export function newMethod(t: Translate, key: TemplateKey = 'epay_custom'): PaymentMethod {
  const { gateway, type } = TEMPLATES[key]
  const name = type ? t(`settings.methods.typeName.${type}`) : ''
  return { type, name, gateway, icon: type, color: brandColor(type), min_topup: 0, enabled: true }
}

/** Largest custom logo, decoded (it is stored inline in the settings). */
export const LOGO_MAX_BYTES = 64 * 1024
/** The file picker's accept list. */
export const LOGO_ACCEPT = Object.keys(LOGO_TYPES).join(',')
const LOGO_MAX_URL = 2048
export const kb = (bytes: number) => Math.ceil(bytes / 1024)

/** Why a picked file cannot be a logo, '' when it can. */
export function logoFileError(file: File, t: Translate): string {
  if (!isLogoType(file.type)) return t('settings.methods.logo.rule.type')
  return file.size > LOGO_MAX_BYTES ? t('settings.methods.logo.rule.size', { size: kb(file.size), max: kb(LOGO_MAX_BYTES) }) : ''
}

/** Rule of method.logo: empty, an allowed image data URL within the size, or an https address. */
export function logoValidator(t: Translate) {
  return (v: string | undefined) => {
    if (!v) return true
    const src = parseLogo(v)
    if (!src) return t('settings.methods.logo.rule.format')
    if (src.kind === 'url') return v.length <= LOGO_MAX_URL || t('settings.methods.logo.rule.urlLength', { max: LOGO_MAX_URL })
    return src.bytes <= LOGO_MAX_BYTES || t('settings.methods.logo.rule.size', { size: kb(src.bytes), max: kb(LOGO_MAX_BYTES) })
  }
}

/** v-bind of an SSwitch at a boolean path. */
export const bindSwitch = (form: SettingsForm, path: string) => ({ modelValue: !!form.get(path), 'onUpdate:modelValue': (v: boolean) => form.set(path, v) })

const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

export function settingsRules(t: Translate): Rules<SettingsModel> {
  const positive = (v: unknown) => !isNum(v) || v > 0 || t('settings.rule.positive')
  const epayOn = (m: SettingsModel) => !!m.epay?.enabled
  const stripeOn = (m: SettingsModel) => !!m.stripe?.enabled
  return {
    currency: { required: true },
    price: [{ required: true, type: 'number' }, { validator: positive }],
    min_topup: { required: true, type: 'number', min: 0 },
    max_topup: [
      { required: true, type: 'number' },
      { validator: (v: number, { model }) => !isNum(model.min_topup) || v > model.min_topup || t('settings.rule.maxAboveMin') }
    ],
    order_timeout_minutes: { required: true, type: 'integer', min: 1 },
    max_pending_orders: { required: true, type: 'integer', min: 1 },
    amount_options: {
      validator: (v: number[] = [], { model }) =>
        v.every((n) => n >= (model.min_topup ?? 0) && n <= (model.max_topup ?? Infinity)) || t('settings.rule.optionRange', { min: model.min_topup, max: model.max_topup })
    },
    'discounts.*.amount': [
      { required: true, type: 'number' },
      { validator: positive },
      { validator: (v: number, { model }) => model.discounts.filter((d) => d.amount === v).length < 2 || t('settings.rule.duplicateTier') }
    ],
    'discounts.*.rate': [{ required: true, type: 'number' }, { validator: (v: number) => (v > 0 && v < 1) || t('settings.rule.rate') }],
    'methods.*.logo': { validator: logoValidator(t) },
    'epay.endpoint': { required: true, type: 'url', when: epayOn },
    'epay.merchant_id': { required: true, whitespace: true, when: epayOn },
    'epay.key': { required: true, when: epayOn },
    'stripe.secret_key': [{ required: true, when: stripeOn }, { pattern: /^(sk|rk)_/, message: () => t('settings.rule.stripeKey') }],
    'stripe.webhook_secret': [{ required: true, when: stripeOn }, { pattern: /^whsec_/, message: () => t('settings.rule.webhookSecret') }]
  }
}
