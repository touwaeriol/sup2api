// Core top-up (CONTRACTS §140): system settings > Top-up settings, Top-up &
// payment > Top-up orders / Promo codes, Mine > Wallet / Top-up history.
export default {
  status: {
    pending: '待支付',
    paid: '已支付',
    completed: '已完成',
    failed: '失败',
    expired: '已过期',
    cancelled: '已取消'
  },
  gateway: {
    epay: '易支付',
    stripe: 'Stripe'
  },
  settings: {
    title: '充值配置',
    tabs: {
      general: '通用',
      methods: '支付方式',
      gateways: '支付网关'
    },
    unit: {
      usd: '美元',
      minutes: '分钟',
      orders: '笔'
    },
    general: {
      recharge: '在线充值',
      limits: '充值限制',
      enabled: {
        label: '启用在线充值',
        help: '用户在钱包里通过下面的支付方式充值余额。'
      },
      currency: {
        label: '结算货币',
        help: '用户实际支付的货币；余额始终以美元计。'
      },
      price: {
        label: '汇率',
        help: '用户充值 1 美元余额需要支付的金额。',
        prefix: '1 美元余额 =',
        example: '示例：充值 100 美元需支付 {pay}。'
      },
      min_topup: {
        label: '最低充值金额',
        help: '单笔订单的最小金额（美元余额）。支付方式可以单独设置更高的下限。'
      },
      max_topup: {
        label: '最高充值金额',
        help: '单笔订单的最大金额（美元余额）。'
      },
      order_timeout_minutes: {
        label: '订单超时',
        help: '超过这个时间未支付的订单自动过期。'
      },
      max_pending_orders: {
        label: '每人待支付订单上限',
        help: '同一用户同时最多有这么多笔未支付的订单。'
      },
      presets: {
        title: '充值选项',
        label: '充值金额预设',
        help: '充值页上可一键选择的金额（美元），用户仍可自行输入。每输入一个按回车或逗号。',
        placeholder: '例如 50，回车添加'
      }
    },
    discounts: {
      title: '充值折扣',
      help: '充值金额达到某档时按该档折扣率计价；同时满足多档时取折扣最大的一档。',
      add: '添加档位',
      empty: '未设置折扣，用户按原价支付。',
      amount: '充值满',
      rate: '折扣率',
      phrase: '折扣',
      example: '示例',
      exampleText: '充 {amount} 美元实付 {pay}（原价 {original}）'
    },
    rule: {
      positive: '必须大于 0',
      maxAboveMin: '必须大于最低充值金额',
      optionRange: '每个预设金额都要在 {min} 到 {max} 之间',
      duplicateTier: '已有相同金额的档位',
      rate: '0 到 1 之间，例如 0.95 表示 9.5 折',
      stripeKey: '以 sk_ 或 rk_ 开头',
      webhookSecret: '以 whsec_ 开头'
    },
    methods: {
      title: '支付方式',
      help: '用户充值时可选的支付方式，按此顺序展示，拖动手柄可调整。每个方式通过一个支付网关收款。',
      templates: '模板',
      add: '添加支付方式',
      empty: '还没有支付方式，可从模板快速添加或手动添加。',
      name: '名称',
      type: '类型键',
      gateway: '所属网关',
      minTopup: '最低充值',
      minGlobal: '使用全局',
      enabled: '启用',
      icon: '图标',
      iconKey: '图标样式',
      color: '颜色',
      taken: '已添加',
      added: '已添加“{name}”',
      gatewayOff: '支付网关“{gateway}”未启用：其下 {count} 个已启用的支付方式不会展示给用户。',
      gatewayOffBadge: '网关未启用',
      toGateways: '前往支付网关',
      noneUsable: '在线充值已开启，但没有网关可用的已启用支付方式，用户暂时无法充值。',
      addTitle: '添加支付方式',
      editTitle: '编辑支付方式',
      unnamed: '未命名',
      previewHint: '用户在钱包里看到的样子',
      nameHint: '展示给用户的名称。',
      namePlaceholder: '例如 支付宝',
      typeHint: '传给网关的支付类型（易支付的 type 或插件网关的方式），可选择或直接输入。',
      typePlaceholder: '选择或输入类型键',
      iconPlaceholder: '选择或输入图标键',
      iconMode: {
        builtin: '内置图标',
        custom: '自定义图片'
      },
      logo: {
        drop: '拖拽图片到这里，或点击选择',
        dropActive: '松开即可上传',
        limits: '支持 PNG、SVG、JPG、WebP，不超过 {max} KB，建议使用正方形图片。',
        uploaded: '{type} 图片 · {size} KB',
        linked: '来自 {host} 的图片',
        replace: '更换',
        remove: '移除',
        url: '或填写图片地址',
        urlPlaceholder: 'https://example.com/logo.png',
        readFailed: '读取文件失败',
        rule: {
          required: '请上传图片或填写图片地址',
          type: '只支持 PNG、SVG、JPG、WebP 图片',
          size: '图片大小 {size} KB，超过上限 {max} KB',
          format: '不是有效的图片：请上传图片或填写 https:// 地址',
          urlLength: '地址不能超过 {max} 个字符'
        }
      },
      minTopupHint: '0 表示使用全局最低充值金额。',
      enabledHint: '关闭后用户看不到这个方式。',
      typeName: {
        alipay: '支付宝',
        wxpay: '微信支付',
        qqpay: 'QQ 钱包',
        stripe: 'Stripe'
      },
      iconName: {
        alipay: '支付宝',
        wxpay: '微信支付',
        qqpay: 'QQ 钱包',
        stripe: 'Stripe',
        card: '银行卡'
      },
      template: {
        epay_alipay: '易支付 · 支付宝',
        epay_wxpay: '易支付 · 微信支付',
        stripe: 'Stripe',
        epay_custom: '自定义易支付方式…'
      },
      rule: {
        typeKey: '1–50 位小写字母、数字或 _',
        typeTaken: '其他支付方式已使用这个类型键',
        color: '颜色格式如 #1677ff'
      }
    },
    gateways: {
      on: '已启用',
      off: '未启用',
      usage: '{count} 个支付方式',
      enabled: '启用',
      needHttps: '站点地址需为 https 才能开通充值，配置后这里显示回调地址。',
      plugin: {
        title: '插件网关',
        badge: '插件',
        help: '由插件“{plugin}”提供。插件负责验签，订单核对和入账仍在核心。',
        ready: '可用',
        down: '插件未启用',
        methods: '提供的支付方式',
        methodsHelp: '和内置网关一样，在“支付方式”标签里添加。',
        settings: '密钥与参数',
        settingsHelp: '保存在插件自己的设置里。',
        open: '插件设置',
        notify: '回调地址',
        notifyHelp: '把这个地址填到支付服务商后台。',
        empty: '还没有插件提供支付网关。可以到插件市场安装，扩展更多支付方式。',
        market: '插件市场'
      },
      epay: {
        help: '兼容易支付协议的聚合支付（支付宝、微信支付、QQ 钱包等）。',
        enabledHelp: '启用后，该网关下的支付方式才会展示给用户。',
        endpoint: '接口地址',
        endpointHelp: '易支付服务商的接口根地址。',
        merchant: '商户号',
        key: '商户密钥',
        keyHelp: '易支付服务商后台提供的商户密钥。',
        notify: '回调地址',
        notifyHelp: '下单时作为 notify_url 发给易支付；服务商要求登记回调地址时填这个。'
      },
      stripe: {
        help: '通过 Stripe Checkout 收取银行卡付款（Apple Pay、Google Pay 也按银行卡）。',
        enabledHelp: '启用后，该网关下的支付方式才会展示给用户。',
        secretKey: 'API 密钥',
        secretKeyHelp: 'sk_live_… 或受限密钥 rk_…。',
        webhookSecret: 'Webhook 签名密钥',
        webhookSecretHelp: '下面这个端点的签名密钥（whsec_…）。',
        webhook: 'Webhook 地址',
        webhookHelp: '在 Stripe 后台添加这个端点，并订阅以下事件：'
      }
    }
  },
  admin: {
    filters: '筛选',
    anyOf: '全部{name}',
    never: '永不过期',
    positive: '必须大于 0',
    futureTime: '必须是将来的时间',
    orders: {
      title: '充值订单',
      search: '订单号、流水号或邮箱',
      empty: '暂无订单',
      detailTitle: '订单详情',
      bonus: '+{amount} 赠送',
      stats: {
        today: '今日充值',
        count: '{n} 笔',
        month: '本月充值',
        pending: '待支付',
        failed: '失败'
      },
      fields: {
        out_trade_no: '订单号',
        user: '用户',
        amount: '充值金额',
        bonus: '赠送',
        pay_amount: '实付',
        method: '支付方式',
        status: '状态',
        promo_code: '优惠码',
        trade_no: '网关流水号',
        failed_reason: '失败原因',
        created_at: '创建时间',
        paid_at: '支付时间',
        completed_at: '完成时间',
        expires_at: '过期时间'
      },
      sections: {
        order: '订单信息',
        payment: '支付信息',
        timeline: '时间'
      },
      events: {
        title: '回调事件',
        empty: '这笔订单还没有回调或查单记录',
        more: '显示最新 {shown} 条，共 {total} 条',
        verified: '已验签',
        unverified: '未验签',
        fields: {
          trade_no: '流水号',
          status: '状态',
          amount: '金额',
          type: '类型',
          error: '错误'
        },
        kind: {
          NOTIFY_OK: '已受理',
          SIGNATURE_INVALID: '签名无效',
          ORDER_NOT_FOUND: '订单不存在',
          PAYMENT_AMOUNT_MISMATCH: '金额不符',
          SECOND_PAYMENT: '重复付款',
          DUPLICATE: '重复通知',
          TRADE_NO_REUSED: '流水号被复用',
          TRADE_NO_MISSING: '缺少流水号',
          GATEWAY_UNAVAILABLE: '网关不可用',
          GATEWAY_MISMATCH: '网关不符',
          MERCHANT_MISMATCH: '商户号不符',
          METHOD_MISMATCH: '支付方式不符',
          SESSION_MISMATCH: '会话不符',
          STATUS_NOT_PAYABLE: '订单不可支付',
          FIELD_TOO_LONG: '字段过长',
          STATUS_UNKNOWN: '未知状态'
        }
      }
    },
    promo: {
      title: '优惠码',
      intro: '用户在充值确认时输入优惠码，支付成功后额外赠送余额。',
      create: '新建优惠码',
      empty: '暂无优惠码',
      anyAmount: '不限',
      usages: '使用记录',
      usage: {
        title: '{code} 的使用记录',
        summary: '共使用 {count} 次（含未付款订单占用的）',
        empty: '还没有人用过这个优惠码',
        fields: {
          user: '用户',
          out_trade_no: '订单号',
          bonus: '赠送',
          status: '状态',
          created_at: '使用时间',
          used_at: '到账时间'
        },
        status: {
          reserved: '占用中（未付款）',
          used: '已到账'
        },
        hint: '占用中的名额会在订单过期或取消时释放。'
      },
      deleteConfirm: '删除后用户不能再使用 {code}，已赠送的余额不受影响。',
      saved: '优惠码已保存',
      status: {
        active: '启用',
        disabled: '停用',
        expired: '已过期',
        exhausted: '已用完'
      },
      fields: {
        code: '优惠码',
        bonus_amount: '赠送金额',
        min_amount: '最低充值',
        usage: '已用 / 上限',
        expires_at: '过期时间',
        status: '状态',
        notes: '备注',
        created_at: '创建时间'
      },
      form: {
        createTitle: '新建优惠码',
        editTitle: '编辑优惠码 {code}',
        code: '优惠码',
        codePlaceholder: '留空自动生成',
        codeHint: '留空自动生成；保存为大写',
        codeFixed: '创建后不能修改',
        codeInvalid: '3–64 位字母、数字、- 或 _',
        bonus: '赠送金额',
        bonusHint: '每笔使用该码的充值额外赠送的余额',
        minAmount: '最低充值',
        minAmountHint: '充值金额达到这个数才能用这个码；0 表示不限',
        maxUses: '使用上限',
        maxUsesHint: '0 表示不限次数',
        expires: '过期时间',
        expiresHint: '留空表示长期有效',
        notes: '备注'
      }
    }
  },
  wallet: {
    title: '充值',
    subtitle: '选择充值金额和支付方式',
    history: '充值记录',
    amount: '充值金额',
    custom: '自定义金额',
    customPlaceholder: '输入充值金额',
    range: '最低 {min}，最高 {max}',
    pay: '实付 {amount}',
    method: '支付方式',
    methodMin: '满 {amount} 可用',
    payable: '实付',
    payNow: '去支付',
    discount: '{zhe} 折',
    unavailable: '暂未开放在线充值',
    unavailableHint: '可以使用兑换码充值，或联系管理员为你加款。',
    amountMin: '最低充值 {amount}',
    amountMax: '最高充值 {amount}',
    confirm: {
      title: '确认支付',
      amount: '充值金额',
      bonus: '赠送',
      credited: '到账',
      original: '原价',
      discount: '折扣',
      pay: '实付',
      method: '支付方式',
      promo: '优惠码',
      promoPlaceholder: '可选',
      apply: '使用',
      applied: '已使用，额外赠送 {amount}',
      submit: '确认支付',
      retry: '重试'
    },
    paying: {
      title: '等待支付',
      orderNo: '订单号',
      pay: '实付金额',
      remaining: '剩余支付时间',
      scan: '使用{method}扫码支付',
      opened: '已在新窗口打开支付页面，支付完成后这里会自动更新。',
      blocked: '浏览器拦截了支付窗口，请点击“重新打开支付页”。',
      reopen: '重新打开支付页',
      cancel: '取消订单',
      cancelConfirm: '确定取消这笔订单？取消后请不要再支付。',
      cancelled: '订单已取消',
      crediting: '已收到付款，正在入账…',
      successTitle: '充值成功',
      success: '已入账 {amount}',
      expired: '订单已超时，请重新下单。',
      expiredLate: '订单已超时。',
      failed: '入账失败：{reason}',
      ended: '订单{status}。',
      done: '完成',
      lateHint: '如果已经付款，仍会自动入账。'
    },
    orders: {
      title: '充值记录',
      subtitle: '你的在线充值订单；待支付的订单在过期前可以继续支付。',
      search: '按订单号搜索',
      anyStatus: '全部状态',
      topup: '去充值',
      no: '订单号',
      amount: '充值金额',
      pay: '实付',
      method: '支付方式',
      status: '状态',
      created: '创建时间',
      completed: '到账时间',
      promo: '优惠码',
      resume: '继续支付',
      gone: '该订单已不能支付，列表已更新为最新状态。',
      empty: '暂无充值记录'
    },
    unavailableHintNoRedeem: '请联系管理员为你加款。'
  }
}
