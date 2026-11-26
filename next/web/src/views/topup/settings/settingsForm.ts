import type { Rules } from '@sub2api/ui'
import type { GatewayKey, PaymentMethod, PaymentSettingsValue } from '../topup'
import { brandColor, isLogoType, LOGO_TYPES, parseLogo } from '../components/methodIcon'

// The admin top-up settings as a form: the model (money as numbers while
// edited), its mapping to and from the wire's decimal strings, the rules,
// tabs, and the catalogs the editors pick from. The server's validation of
// PUT /topup/settings is the final check.

type Translate = (key: string, params?: Record<string, unknown>) => string

/** A payment method as edited: its minimum as a number. */
export type MethodModel = Omit<PaymentMethod, 'min_topup'> & { min_topup?: number }

/** PaymentSettingsValue with numbers for the amounts, prices and rates. */
export interface SettingsModel
  extends
    Omit<PaymentSettingsValue, 'price' | 'min_topup' | 'max_topup' | 'amount_options' | 'discounts' | 'methods'>,
    Record<string, unknown> {
  price: number
  min_topup: number
  max_topup: number
  amount_options: number[]
  discounts: { amount: number; rate: number }[]
  methods: MethodModel[]
}

export type SettingsTab = 'general' | 'methods' | 'gateways'
export const SETTINGS_TABS: SettingsTab[] = ['general', 'methods', 'gateways']

/** Top-level keys outside the general tab. */
const TAB_OF_KEY: Record<string, SettingsTab> = { methods: 'methods', epay: 'gateways', stripe: 'gateways' }

/** The tab showing a field path ('epay.key' -> gateways). */
export const tabOfPath = (path: string): SettingsTab => TAB_OF_KEY[path.split(/[.[]/)[0]] ?? 'general'

/** The form before the settings are read (the page shows a spinner until then). */
export const DEFAULT_MODEL = (): SettingsModel => ({
  enabled: false,
  currency: 'CNY',
  price: 0,
  min_topup: 0,
  max_topup: 0,
  order_timeout_minutes: 30,
  max_pending_orders: 3,
  amount_options: [],
  discounts: [],
  methods: [],
  epay: { enabled: false, endpoint: '', merchant_id: '', key: '' },
  stripe: { enabled: false, secret_key: '', webhook_secret: '' }
})

/** The values read, as the form edits them. */
export function toModel(v: PaymentSettingsValue): SettingsModel {
  return {
    ...v,
    price: Number(v.price),
    min_topup: Number(v.min_topup),
    max_topup: Number(v.max_topup),
    amount_options: v.amount_options.map(Number),
    discounts: v.discounts.map(d => ({ amount: Number(d.amount), rate: Number(d.rate) })),
    methods: v.methods.map(m => ({ ...m, min_topup: Number(m.min_topup ?? 0) }))
  }
}

/** The form as PUT /topup/settings takes it: amounts as decimal strings. */
export function toValues(m: SettingsModel): PaymentSettingsValue {
  return {
    ...m,
    price: String(m.price),
    min_topup: String(m.min_topup),
    max_topup: String(m.max_topup),
    amount_options: m.amount_options.map(String),
    discounts: m.discounts.map(d => ({ amount: String(d.amount), rate: String(d.rate) })),
    methods: m.methods.map(x => ({ ...x, min_topup: String(x.min_topup ?? 0) }))
  }
}

/** Type keys each gateway knows; epay also takes custom ones (its `type` parameter). */
export const GATEWAY_TYPES: Record<GatewayKey, string[]> = { epay: ['alipay', 'wxpay', 'qqpay'], stripe: ['stripe'] }

const KNOWN_TYPES = Object.values(GATEWAY_TYPES).flat()
/** A type key with a translated name (settings.methods.typeName.*). */
export const isKnownType = (type: string) => KNOWN_TYPES.includes(type)

/** A method's type key and name as the core accepts them (PUT /topup/settings). */
export const METHOD_TYPE_PATTERN = /^[a-z0-9_]{1,50}$/
export const METHOD_NAME_MAX = 100

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
export function newMethod(t: Translate, key: TemplateKey = 'epay_custom'): MethodModel {
  const { gateway, type } = TEMPLATES[key]
  const name = type ? t(`topup.settings.methods.typeName.${type}`) : ''
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
  if (!isLogoType(file.type)) return t('topup.settings.methods.logo.rule.type')
  return file.size > LOGO_MAX_BYTES ? t('topup.settings.methods.logo.rule.size', { size: kb(file.size), max: kb(LOGO_MAX_BYTES) }) : ''
}

/** Rule of method.logo: empty, an allowed image data URL within the size, or an https address. */
export function logoValidator(t: Translate) {
  return (v: string | undefined) => {
    if (!v) return true
    const src = parseLogo(v)
    if (!src) return t('topup.settings.methods.logo.rule.format')
    if (src.kind === 'url') return v.length <= LOGO_MAX_URL || t('topup.settings.methods.logo.rule.urlLength', { max: LOGO_MAX_URL })
    return src.bytes <= LOGO_MAX_BYTES || t('topup.settings.methods.logo.rule.size', { size: kb(src.bytes), max: kb(LOGO_MAX_BYTES) })
  }
}

const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

const positiveRule = (t: Translate) => (v: unknown) => !isNum(v) || v > 0 || t('topup.settings.rule.positive')

/** Rules of the general tab's scalars. */
function generalRules(t: Translate): Rules<SettingsModel> {
  return {
    currency: { required: true },
    price: [{ required: true, type: 'number' }, { validator: positiveRule(t) }],
    min_topup: { required: true, type: 'number', min: 0 },
    max_topup: [
      { required: true, type: 'number' },
      { validator: (v: number, { model }) => !isNum(model.min_topup) || v > model.min_topup || t('topup.settings.rule.maxAboveMin') }
    ],
    order_timeout_minutes: { required: true, type: 'integer', min: 1 },
    max_pending_orders: { required: true, type: 'integer', min: 1 }
  }
}

/** Rules of the lists: amount presets, discount tiers and the methods' logos. */
function listRules(t: Translate): Rules<SettingsModel> {
  return {
    amount_options: {
      validator: (v: number[] = [], { model }) =>
        v.every(n => n >= (model.min_topup ?? 0) && n <= (model.max_topup ?? Infinity)) ||
        t('topup.settings.rule.optionRange', { min: model.min_topup, max: model.max_topup })
    },
    'discounts.*.amount': [
      { required: true, type: 'number' },
      { validator: positiveRule(t) },
      {
        validator: (v: number, { model }) =>
          model.discounts.filter(d => d.amount === v).length < 2 || t('topup.settings.rule.duplicateTier')
      }
    ],
    'discounts.*.rate': [
      { required: true, type: 'number' },
      { validator: (v: number) => (v > 0 && v < 1) || t('topup.settings.rule.rate') }
    ],
    'methods.*.logo': { validator: logoValidator(t) }
  }
}

/** Rules of the built-in gateways' credentials, required while the gateway is on. */
function gatewayRules(t: Translate): Rules<SettingsModel> {
  const epayOn = (m: SettingsModel) => !!m.epay?.enabled
  const stripeOn = (m: SettingsModel) => !!m.stripe?.enabled
  return {
    'epay.endpoint': { required: true, type: 'url', when: epayOn },
    'epay.merchant_id': { required: true, whitespace: true, when: epayOn },
    'epay.key': { required: true, when: epayOn },
    'stripe.secret_key': [
      { required: true, when: stripeOn },
      { pattern: /^(sk|rk)_/, message: () => t('topup.settings.rule.stripeKey') }
    ],
    'stripe.webhook_secret': [
      { required: true, when: stripeOn },
      { pattern: /^whsec_/, message: () => t('topup.settings.rule.webhookSecret') }
    ]
  }
}

export function settingsRules(t: Translate): Rules<SettingsModel> {
  return { ...generalRules(t), ...listRules(t), ...gatewayRules(t) }
}
