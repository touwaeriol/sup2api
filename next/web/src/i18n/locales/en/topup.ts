// Core top-up (CONTRACTS §140): system settings > Top-up settings, Top-up &
// payment > Top-up orders / Promo codes, Mine > Wallet / Top-up history.
export default {
  status: {
    pending: 'Pending',
    paid: 'Paid',
    completed: 'Completed',
    failed: 'Failed',
    expired: 'Expired',
    cancelled: 'Cancelled'
  },
  gateway: {
    epay: 'Epay',
    stripe: 'Stripe'
  },
  settings: {
    title: 'Top-up settings',
    tabs: {
      general: 'General',
      methods: 'Payment methods',
      gateways: 'Gateways'
    },
    unit: {
      usd: 'USD',
      minutes: 'min',
      orders: 'orders'
    },
    general: {
      recharge: 'Online top-up',
      limits: 'Limits',
      enabled: {
        label: 'Enable online top-up',
        help: 'Users top up their balance in the wallet with the methods below.'
      },
      currency: {
        label: 'Payment currency',
        help: 'The currency users pay in; balance is always in USD.'
      },
      price: {
        label: 'Exchange rate',
        help: 'What users pay for 1 USD of balance.',
        prefix: '1 USD =',
        example: 'Topping up 100 USD costs {pay}.'
      },
      min_topup: {
        label: 'Minimum top-up',
        help: 'Smallest amount per order, in balance (USD). A method may set a higher one.'
      },
      max_topup: {
        label: 'Maximum top-up',
        help: 'Largest amount per order, in balance (USD).'
      },
      order_timeout_minutes: {
        label: 'Order timeout',
        help: 'Unpaid orders expire after this long.'
      },
      max_pending_orders: {
        label: 'Pending orders per user',
        help: 'A user cannot open more unpaid orders than this at a time.'
      },
      presets: {
        title: 'Top-up options',
        label: 'Amount presets',
        help: 'Amounts (USD) offered as one-click choices; users can still type their own. Press Enter or a comma after each.',
        placeholder: 'e.g. 50, then Enter'
      }
    },
    discounts: {
      title: 'Top-up discounts',
      help: 'From the amount up, users pay the rate of the price. When several tiers apply, the lowest rate wins.',
      add: 'Add tier',
      empty: 'No discounts: users pay the full price.',
      amount: 'From',
      rate: 'Rate',
      phrase: 'Discount',
      example: 'Example',
      exampleText: '{amount} USD costs {pay} (was {original})'
    },
    rule: {
      positive: 'Must be greater than 0',
      maxAboveMin: 'Must be greater than the minimum top-up',
      optionRange: 'Every preset must be between {min} and {max}',
      duplicateTier: 'Another tier has this amount',
      rate: 'Between 0 and 1, e.g. 0.95 for 5% off',
      stripeKey: 'Starts with sk_ or rk_',
      webhookSecret: 'Starts with whsec_'
    },
    methods: {
      title: 'Payment methods',
      help: 'What users pick when topping up, in this order; drag the handle to reorder. Each method is paid through one gateway.',
      templates: 'Templates',
      add: 'Add method',
      empty: 'No payment methods yet. Add one from a template or by hand.',
      name: 'Name',
      type: 'Type key',
      gateway: 'Gateway',
      minTopup: 'Minimum top-up',
      minGlobal: 'Global',
      enabled: 'Enabled',
      icon: 'Icon',
      iconKey: 'Icon style',
      color: 'Color',
      taken: 'added',
      added: '{name} added',
      gatewayOff: 'The {gateway} gateway is off: its enabled methods ({count}) are not shown to users.',
      gatewayOffBadge: 'Gateway off',
      toGateways: 'Open gateways',
      noneUsable: 'Online top-up is on, but no enabled method has its gateway on: users cannot top up.',
      addTitle: 'Add payment method',
      editTitle: 'Edit payment method',
      unnamed: 'Unnamed method',
      previewHint: 'As users see it in the wallet',
      nameHint: 'Shown to users.',
      namePlaceholder: 'e.g. Alipay',
      typeHint: "Sent to the gateway (the epay type, or a plugin gateway's method). Pick one or type your own.",
      typePlaceholder: 'Pick or type a key',
      iconPlaceholder: 'Pick or type an icon key',
      iconMode: {
        builtin: 'Built-in icon',
        custom: 'Custom image'
      },
      logo: {
        drop: 'Drag an image here, or click to choose',
        dropActive: 'Drop the image to upload',
        limits: 'PNG, SVG, JPG or WebP, up to {max} KB. Square images look best.',
        uploaded: '{type} image · {size} KB',
        linked: 'Image from {host}',
        replace: 'Replace',
        remove: 'Remove',
        url: 'Or use an image address',
        urlPlaceholder: 'https://example.com/logo.png',
        readFailed: 'Could not read the file',
        rule: {
          required: 'Upload an image or enter its address',
          type: 'Only PNG, SVG, JPG or WebP images',
          size: 'The image is {size} KB; the limit is {max} KB',
          format: 'Not an image: upload one or enter an https:// address',
          urlLength: 'The address is longer than {max} characters'
        }
      },
      minTopupHint: '0 uses the global minimum.',
      enabledHint: 'Off hides it from users.',
      typeName: {
        alipay: 'Alipay',
        wxpay: 'WeChat Pay',
        qqpay: 'QQ Wallet',
        stripe: 'Stripe'
      },
      iconName: {
        alipay: 'Alipay',
        wxpay: 'WeChat Pay',
        qqpay: 'QQ Wallet',
        stripe: 'Stripe',
        card: 'Bank card'
      },
      template: {
        epay_alipay: 'Epay · Alipay',
        epay_wxpay: 'Epay · WeChat Pay',
        stripe: 'Stripe',
        epay_custom: 'Custom Epay method…'
      },
      rule: {
        typeKey: '1 to 50 lowercase letters, digits or _',
        typeTaken: 'Another method uses this key',
        color: 'A color like #1677ff'
      }
    },
    gateways: {
      on: 'On',
      off: 'Off',
      usage: 'Methods: {count}',
      enabled: 'Enable',
      needHttps: 'The site address must be https to open top-up; the callback address shows once it is.',
      plugin: {
        title: 'Plugin gateways',
        badge: 'Plugin',
        help: 'Provided by the plugin {plugin}. It verifies the notifications; the order checks and the crediting stay in the core.',
        ready: 'Ready',
        down: 'Plugin off',
        methods: 'Methods it offers',
        methodsHelp: 'Add them on the Payment methods tab like the built-in ones.',
        settings: 'Credentials',
        settingsHelp: "Kept in the plugin's own settings.",
        open: 'Plugin settings',
        notify: 'Notify URL',
        notifyHelp: 'Give this address to the payment provider.',
        empty: 'No plugin adds a payment gateway yet. Install one from the plugin market to offer more ways to pay.',
        market: 'Plugin market'
      },
      epay: {
        help: 'Aggregated payments over the Epay protocol (Alipay, WeChat Pay, QQ Wallet …).',
        enabledHelp: 'Methods of this gateway are offered only while it is on.',
        endpoint: 'API address',
        endpointHelp: 'The base address of your Epay provider.',
        merchant: 'Merchant ID',
        key: 'Merchant key',
        keyHelp: 'The merchant key from your Epay provider.',
        notify: 'Callback address',
        notifyHelp: 'Sent with each order as notify_url; allow it at your provider if it keeps a list.'
      },
      stripe: {
        help: 'Card payments through Stripe Checkout (Apple Pay and Google Pay count as cards).',
        enabledHelp: 'Methods of this gateway are offered only while it is on.',
        secretKey: 'Secret key',
        secretKeyHelp: 'sk_live_… or a restricted rk_… key.',
        webhookSecret: 'Webhook signing secret',
        webhookSecretHelp: 'Signing secret (whsec_…) of the endpoint below.',
        webhook: 'Webhook endpoint',
        webhookHelp: 'Add this endpoint in the Stripe dashboard and subscribe to:'
      }
    }
  },
  admin: {
    filters: 'Filters',
    anyOf: 'Any {name}',
    never: 'Never',
    positive: 'Must be greater than 0',
    futureTime: 'Must be a time in the future',
    orders: {
      title: 'Top-up orders',
      search: 'Order no., transaction no. or email',
      empty: 'No orders',
      detailTitle: 'Order details',
      bonus: '+{amount} bonus',
      stats: {
        today: 'Topped up today',
        count: '{n} orders',
        month: 'This month',
        pending: 'Pending',
        failed: 'Failed'
      },
      fields: {
        out_trade_no: 'Order number',
        user: 'User',
        amount: 'Amount',
        bonus: 'Bonus',
        pay_amount: 'Paid',
        method: 'Method',
        status: 'Status',
        promo_code: 'Promo code',
        trade_no: 'Gateway transaction no.',
        failed_reason: 'Failure reason',
        created_at: 'Created',
        paid_at: 'Paid at',
        completed_at: 'Completed',
        expires_at: 'Expires'
      },
      sections: {
        order: 'Order',
        payment: 'Payment',
        timeline: 'Timeline'
      },
      events: {
        title: 'Gateway notifications',
        empty: 'No notification or query recorded for this order',
        more: 'Showing the latest {shown} of {total}',
        verified: 'Verified',
        unverified: 'Not verified',
        fields: {
          trade_no: 'Transaction',
          status: 'Status',
          amount: 'Amount',
          type: 'Type',
          error: 'Error'
        },
        kind: {
          NOTIFY_OK: 'Accepted',
          SIGNATURE_INVALID: 'Bad signature',
          ORDER_NOT_FOUND: 'Order not found',
          PAYMENT_AMOUNT_MISMATCH: 'Amount mismatch',
          SECOND_PAYMENT: 'Second payment',
          DUPLICATE: 'Duplicate',
          TRADE_NO_REUSED: 'Transaction no. reused',
          TRADE_NO_MISSING: 'Transaction no. missing',
          GATEWAY_UNAVAILABLE: 'Gateway unavailable',
          GATEWAY_MISMATCH: 'Other gateway',
          MERCHANT_MISMATCH: 'Merchant mismatch',
          METHOD_MISMATCH: 'Method mismatch',
          SESSION_MISMATCH: 'Session mismatch',
          STATUS_NOT_PAYABLE: 'Order not payable',
          FIELD_TOO_LONG: 'Field too long',
          STATUS_UNKNOWN: 'Unknown status'
        }
      }
    },
    promo: {
      title: 'Promo codes',
      intro: 'Users enter a promo code when confirming a top-up; once the payment succeeds they are credited the bonus on top.',
      create: 'New promo code',
      empty: 'No promo codes',
      anyAmount: 'Any',
      usages: 'Usage',
      usage: {
        title: 'Usage of {code}',
        summary: 'Used {count} times, unpaid orders holding a use included',
        empty: 'Nobody has used this code yet',
        fields: {
          user: 'User',
          out_trade_no: 'Order',
          bonus: 'Bonus',
          status: 'Status',
          created_at: 'Applied',
          used_at: 'Credited'
        },
        status: {
          reserved: 'Held (unpaid)',
          used: 'Credited'
        },
        hint: 'A held use is released when its order expires or is cancelled.'
      },
      deleteConfirm: 'Users can no longer use {code}. Bonuses already credited stay.',
      saved: 'Promo code saved',
      status: {
        active: 'Enabled',
        disabled: 'Disabled',
        expired: 'Expired',
        exhausted: 'Used up'
      },
      fields: {
        code: 'Code',
        bonus_amount: 'Bonus',
        min_amount: 'Min top-up',
        usage: 'Used / limit',
        expires_at: 'Expires',
        status: 'Status',
        notes: 'Notes',
        created_at: 'Created'
      },
      form: {
        createTitle: 'New promo code',
        editTitle: 'Edit promo code {code}',
        code: 'Code',
        codePlaceholder: 'Generated when empty',
        codeHint: 'Leave empty to generate one; saved in upper case',
        codeFixed: 'A code cannot be changed once created',
        codeInvalid: '3 to 64 letters, digits, - or _',
        bonus: 'Bonus',
        bonusHint: 'Balance credited on top of each top-up using the code',
        minAmount: 'Minimum top-up',
        minAmountHint: 'The code applies only to top-ups of at least this amount; 0 for any amount',
        maxUses: 'Usage limit',
        maxUsesHint: '0 for unlimited uses',
        expires: 'Expires',
        expiresHint: 'Leave empty for a code that never expires',
        notes: 'Notes'
      }
    }
  },
  wallet: {
    title: 'Top up',
    subtitle: 'Choose an amount and a payment method',
    history: 'Top-up history',
    amount: 'Amount',
    custom: 'Custom amount',
    customPlaceholder: 'Enter an amount',
    range: 'Min {min}, max {max}',
    pay: 'Pay {amount}',
    method: 'Payment method',
    methodMin: 'From {amount}',
    payable: 'To pay',
    payNow: 'Pay now',
    discount: '{off}% off',
    unavailable: 'Online top-up is not available yet',
    unavailableHint: 'Use a redeem code, or ask an administrator to add funds.',
    amountMin: 'At least {amount}',
    amountMax: 'At most {amount}',
    confirm: {
      title: 'Confirm payment',
      amount: 'Top-up amount',
      bonus: 'Bonus',
      credited: 'Credited',
      original: 'Original price',
      discount: 'Discount',
      pay: 'To pay',
      method: 'Payment method',
      promo: 'Promo code',
      promoPlaceholder: 'Optional',
      apply: 'Apply',
      applied: 'Applied: {amount} extra credit',
      submit: 'Confirm payment',
      retry: 'Try again'
    },
    paying: {
      title: 'Waiting for payment',
      orderNo: 'Order number',
      pay: 'Amount to pay',
      remaining: 'Time left',
      scan: 'Scan with {method}',
      opened: 'The payment page opened in a new window. This dialog updates by itself once you have paid.',
      blocked: 'The browser blocked the payment window. Use "Reopen payment page".',
      reopen: 'Reopen payment page',
      cancel: 'Cancel order',
      cancelConfirm: 'Cancel this order? Do not pay it afterwards.',
      cancelled: 'Order cancelled',
      crediting: 'Payment received, crediting your balance…',
      successTitle: 'Top-up complete',
      success: '{amount} credited to your balance',
      expired: 'The order timed out. Place a new one to top up.',
      expiredLate: 'The order timed out.',
      failed: 'Crediting failed: {reason}',
      ended: 'This order is {status}.',
      done: 'Done',
      lateHint: 'If you have already paid, the payment is still credited automatically.'
    },
    orders: {
      title: 'Top-up history',
      subtitle: 'Your online top-up orders; a pending order can be paid again until it expires.',
      search: 'Search by order number',
      anyStatus: 'All statuses',
      topup: 'Top up',
      no: 'Order number',
      amount: 'Amount',
      pay: 'Paid',
      method: 'Method',
      status: 'Status',
      created: 'Created',
      completed: 'Credited',
      promo: 'Promo code',
      resume: 'Continue paying',
      gone: 'This order can no longer be paid; the list shows how it stands now.',
      empty: 'No top-ups yet'
    },
    unavailableHintNoRedeem: 'Ask an administrator to add funds.'
  }
}
