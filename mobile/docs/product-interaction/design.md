# Mobile product interaction

> 状态：Android-first Flutter 页面已实现配对、工作台、锚定电脑选择菜单、电脑管理页（排序/启停/名称与 Relay 编辑）、项目分页、项目内新建对话、分页会话详情、大内容查看、局部重试和受控写入首版；完整搜索/action schema、应用锁、iOS 与全量真机验收尚未完成。

## 目标与范围

让用户在手机上用很少的状态判断完成三件事：知道电脑和任务是否仍在运行、在需要时安全地处理 action、向已有 thread 继续输入。交互必须诚实表达在线性和确认程度，不能把“已路由”包装为“已执行”。

## 交互原则

- **状态先于内容**：页面顶部始终可见当前 Desktop、连接状态和最后同步时间。
- **危险操作二次确认**：拒绝 approval、中断 Turn、解除配对需要确认；普通文字发送不增加多余确认。
- **读写分离**：离线仍可看到设备元数据和清晰空态，但所有写入口关闭，不创建本地 outbox。
- **渐进披露**：默认展示适合手机阅读的消息/工具摘要；reasoning 完整展示，Plan 和大内容通过专用查看器渐进加载，工具参数和输出不进入 Mobile Timeline。
- **隐私默认收敛**：通知、系统任务切换预览和日志不出现 Prompt/action 正文；用户进入并解锁 App 后才显示内容。

## 界面配色

运行界面采用中性灰阶分层，统一颜色入口为 [`theme.dart`](../../app/lib/app/theme.dart)。浅色页面背景为 `#F3F3F3`；标题胶囊、返回/操作按钮、输入面板和弹层为白色，胶囊与输入面板以 `#DEDEDE` 细边框及柔和阴影区分背景。表单输入使用浅灰填充和细边框，获得焦点时使用深色边框；会话输入在白色面板内保持透明。正文为 `#111111`，次要文字与占位提示为 `#68686D`。发送与常规主按钮使用深色底和浅色图标，普通发送按钮禁用态使用灰底及灰色图标；中断按钮始终保留与可用态一致的底色和图标色，离线或提交期间仍按原有规则禁用点击。蓝色继续用于运行状态与专用动作，绿色表示在线，橙色表示待处理。

深色主题对应使用深色页面、较亮浮层、灰色边框和浅色主按钮。验收时核对浅色/深色的标题、输入、启用/禁用按钮，以及键盘展开后的面板边界。调整主题色或共享控件时必须同步更新本节。

## 应用图标

Android/iOS 启动图标以 [`app/branding/launcher-icon.svg`](../../app/branding/launcher-icon.svg) 为源：白色背景、黑色 WAW 连线、绿色中心点。Android 的 `mipmap-*/ic_launcher.png` 和 iOS 的 `AppIcon.appiconset` 均由该图形按平台要求输出。应用内配对页、工作台顶栏和空白会话使用独立的 [`app/assets/waw-app-icon.svg`](../../app/assets/waw-app-icon.svg)，保持黑底白色连线；更改启动图标时不改变应用内页面。发布前需在 Android 启动器及 iOS 主屏幕核对图标的背景、颜色和裁切。

## 信息架构

App 不使用底部导航栏。无 pairing 时根页面是添加配对；至少一个 pairing 后根页面是单一工作台，详情使用系统返回栈逐级下钻：

```text
添加配对（无 Daemon）

工作台（有 Daemon）
  ├─ 左上角 App icon
  ├─ 顶栏中间活跃任务提示
  ├─ 顶栏右侧 Daemon selector
  ├─ 下一行横向 Agent selector
  ├─ 项目列表 / Hermes Bot 列表
  ├─ 最近对话列表
  └─ 项目与对话搜索
       ├─ 项目详情 → 项目对话列表
       └─ 对话详情 → Timeline / Composer / Action
```

Android 页面采用 edge-to-edge，顶部系统状态栏保持透明并叠加在页面背景上；浅色主题使用深色系统图标，深色主题使用浅色系统图标，启动主题与 Flutter 首帧保持同样的图标亮度。页面内容通过 `SafeArea` 避让系统状态栏，系统栏图标不由 App 自绘。

配对管理、通知、隐私和诊断是低频操作，从 Daemon selector 底部的“添加电脑 / 管理电脑”和页面 overflow 的“设置”进入；待处理 action 不成为独立一级页，而是在关联对话卡片、会话详情和顶部待处理入口中呈现。

手机工作台的基准布局：

```text
┌──────────────────────────────────┐
│ [App]   [2 个待处理] [● My PC ▾] │  活跃任务 + Daemon
│ [Codex 1] [Codex 2] [Claude…] → │  Agent 横向列表
│ 最近项目                         │
│ Project A                 2 分钟 │
│ Project B                 昨天   │
│          [展开更多项目]          │
│                                  │
│ 最近对话                         │
│ 修复登录流程              ◌ 运行中│  蓝色 loading
│ Review relay protocol     10 分钟│
│                                  │
│ [ 搜索项目和对话              ]  │
└──────────────────────────────────┘
```

底部没有导航栏；搜索框固定在工作台内容底部，但不承担页面切换或一级导航职责。搜索区域沿用页面背景，仅输入框显示浮层填充、细边框与阴影，不使用额外的 BottomSheet 圆角底板；搜索栏独立占位，列表末项不被覆盖，并通过 SafeArea 避让系统底部区域。键盘展开时，搜索栏按系统 viewInsets.bottom 上移到键盘上方，同时缩小列表可用区域；键盘收起后恢复底部位置。输入文字为 16sp，搜索图标为 22dp，输入框垂直内边距为 12dp，高度随动态字体增长。项目对话列表页与具体会话页复用 WawBackButton：48dp 圆形触控区域、24dp 箭头，左侧距离页面边缘 12dp，与具体会话页右侧操作胶囊的右边距共用同一个常量，位于系统状态栏下方 72dp 高工具栏内垂直居中；两页按钮位置和大小完全一致。

顶栏需要适配窄屏和动态字体：品牌名允许单行省略，Daemon selector 使用可收缩的有限宽度，电脑名允许视觉省略但必须保留完整无障碍朗读。顶栏采用左侧固定 App icon、中间独立活跃任务区、右侧固定 Daemon selector 的三段布局；左侧 Logo 保持 24dp 布局边距；Daemon selector 的右边距为 20dp，以补偿长圆胶囊端部弧线带来的视觉留白，任务提示不得覆盖 selector 或被 selector 遮挡。窄屏且存在运行中对话时，提示优先保留 loading icon，并在可用宽度内显示可省略的运行数量；完整语义由无障碍标签和详情页提供。

### Daemon selector

工作台顶栏右侧显示当前 daemon 所在电脑的主机名和状态 icon。主机名由配对二维码的 `computerName` 提供，仅在配对成功后读取一次并保存；后续连接恢复、刷新和状态探测不重新读取或覆盖该名称，以便管理电脑功能后续修改名称。顶栏电脑选择区域使用与标题/输入面板一致的浮层胶囊：浅色主题为白色填充、细边框和柔和阴影，深色主题为对应浮层色，保持与页面背景可辨；胶囊内只显示状态点、电脑名和下拉箭头，名称直接占满剩余宽度，名称与箭头间距为 4dp，不显示“在线”或“安全连接”文字。没有已保存名称时使用本地化的通用“电脑”文案。默认恢复上次成功选择的 Daemon；该记录已不存在或已 revoke 时选择第一条可用 pairing，没有可用 pairing 时回到添加配对页。左侧始终显示 Work Anywhere App icon；不额外显示品牌名。

顶栏中间使用条件式任务提示：存在待处理 action 时优先显示“n 个待处理”，否则存在运行中对话时显示蓝色 loading icon 和“n 个运行中”；点击进入对应的第一条对话。没有活跃任务时不保留空提示控件，左侧只保留 App icon。

点击后使用锚定在选择器下方的 menu 展示所有已启用 Daemon；菜单最大高度受屏幕限制并可滚动：

菜单使用不透明页面表面，点击外部或系统返回关闭；底部搜索框等常驻控件不参与定位，也不得遮挡菜单。

| 状态 | 图形 | 语义 |
| --- | --- | --- |
| `connecting/authenticating/handshaking/syncing` | 旋转 loading icon | 正在连接或同步 |
| `online` | 绿色实心点 | 当前探测在线且安全会话可用 |
| `offline/disconnected` | 灰色实心点 | 当前不可达或尚未连接 |

下拉项图形旁同时显示“连接中 / 在线 / 离线”，屏幕阅读器读取同一状态，不能只依赖颜色；顶栏状态点也必须通过无障碍 label 暴露状态。会话页标题下方复用实时 transport 状态；收到 disconnected/closed/revoked、`peer_offline` 或同步读确认离线时立即由绿色“安全连接”切换为灰色“离线”并禁用写操作，成功恢复读或 online event 后再恢复绿色。选中的 Daemon 有 check mark。切换结果由 Core 的 activeDaemonId/generation 决定；收到新选择后清空旧 Daemon 内容并加载，新目标完成同步前写操作不可用，旧请求的迟到结果不得覆盖新工作台。未选中的 Daemon 在 App 前台以有界短连接依次探测，列表状态带最后检查时间，不把历史在线状态冒充实时状态。

### Agent selector

Daemon 顶栏下一行横向展示该电脑上可发现的 Agent，例如 `Codex 1`、`Codex 2`、`Claude Code`、`Hermes`、`OpenCode`、`Pi`；超出屏幕宽度时左右滑动。默认恢复“该 Daemon 上次选择的 Agent”；已不存在时选择发现列表中的第一个 Agent，不因其他 Agent 处于 ready 状态而自动切换。

Agent selector 每项至少显示 display name 和 provider icon/label；当前项使用明确选中态，未适配项显示“预留”。V1 只完整适配 Codex；未适配 Provider 可以被发现，但选择后进入只读说明页，不展示无法工作的项目或发送入口。Agent 列表与当前 Agent 的项目、对话同步结果分别保留；当前 Agent 内容同步失败时保留整个 selector，只清空当前 Agent 内容并显示重试提示。切换到其他 Agent 时立即更新选择并重新独立加载其项目和对话，前一个 Agent 的迟到结果或失败不得覆盖当前 Agent。切换 Agent 与切换 Daemon 一样清空旧 selection generation 的内容，且每个 Daemon 分别持久化上次 Agent 选择。

## 首次使用与配对

### Onboarding

1. 欢迎页用一句话说明“手机通过你的电脑运行 Codex，Relay 看不到会话正文”。主按钮“连接电脑”，次按钮“查看原理”。
2. 选择“扫描二维码”或“粘贴配对链接”。相机权限只在用户选择扫描后请求；拒绝后仍提供粘贴路径。
3. 扫码解析或配对传输失败停留在 pairing 页面并恢复扫码/粘贴入口，不能进入工作台级错误页；过期、非 `waw://pair`、Relay 地址不可达分别给具体原因，并对 HTTP Relay 提示明文传输风险。

### Candidate review

由于二维码本身尚未被 Mobile 信任，确认页只显示：Relay 域名、二维码到期倒计时、双方 key 的短 fingerprint 和 Mobile display name。按钮文案是“向电脑发起连接”，不是“信任此电脑”。

提交后进入全屏等待页：

```text
等待电脑确认
请在 Work Anywhere Desktop 中核对指纹并批准
02:43

[取消]
```

批准后先持久化已验证 pairing，再同步工作台。全部标准 scope 齐全时进入工作台并选中新配对的 Daemon；首次 Agent/项目/对话同步失败时仍保留 pairing 和 Daemon 工作台，以 banner 提供重试，不能回退成未配对页。缺少关键 scope 时仍可进入，但在一次性说明页解释只读/受限能力。拒绝、Desktop 离线、尝试过多、过期分别提供“重新扫描”或“返回”，不自动复用 secret。

## 工作台内容

页面固定顺序为：App icon + 居中活跃任务提示 + 右侧 Daemon selector 顶栏 → 横向 Agent selector → Provider 主对象列表 → 最近对话 → 底部搜索框。顶栏和 Agent selector 固定在工作台顶部，不随纵向列表滚动；纵向滚动与下拉刷新只作用于 Provider 主对象和最近对话内容区。下拉刷新重建当前 Daemon/Agent snapshot，不打断运行中的对话。

### Codex 项目

Codex Agent 下默认展示最近活跃的 10 个项目，按最近一条对话更新时间倒序。项目使用约 26dp 线框文件夹图标、正常字重名称和约 54dp 的紧凑纯文本行，不使用卡片或分割线；Mobile DTO 不携带本机根路径。项目超过 10 个时列表底部显示“展开更多项目”，每次追加一页且保持已加载顺序；没有更多数据时移除入口，不显示无效按钮。

点击项目进入项目详情，默认加载该项目最近 15 条对话，按最后更新时间倒序；底部使用“显示更多”继续分页。项目页标题下显示 Agent，不展示 Desktop 本机路径；返回后恢复工作台滚动位置、已展开项目数量和搜索前状态。

项目详情页采用圆形返回按钮、项目名称和当前电脑/连接状态标题，右下角显示蓝色圆形铅笔图标按钮；按钮不显示文字，使用 tooltip 和无障碍标签表达“新建对话”。点击只打开当前项目的空白会话详情页，不立即访问 Desktop；页面中央显示 WAW logo 和项目名，底部显示 Composer。用户第一次发送文字或图片时，客户端才通过 `conversation.create` 创建当前项目的 Codex thread，并随后发送第一条 Turn。创建/发送期间输入区显示 loading 且不可重复提交；失败时保留空白页和输入内容，不创建本地 outbox。

### Hermes Bot

Provider capability 为 `bots` 时，同一位置展示 Bot 列表而不是项目列表。Hermes 的 Bot 数据、排序与详情属于后续适配；Codex-first V1 不伪造 Bot 为项目，也不让未实现的 Hermes chip进入空白项目页。

### 最近对话

项目/Bot 区域下方展示当前 Agent 最近 20 条对话，跨项目合并且不按项目分组，按最后更新时间倒序。对话使用与项目一致的约 54dp 紧凑扁平文本行，左侧单行标题、右侧相对时间；项目详情中的对话列表沿用相同行高。列表 DTO 不携带 preview，状态优先级为 `等待处理 > 运行中 > 正在停止 > 失败 > 空闲`。
`needsAttention=true` 时标题右侧显示橙色描边感叹号并提供“需要人工处理”的无障碍文本，取代运行中 loading 和相对时间；工作台顶栏同时优先显示“n 个待处理”。

运行中的对话在卡片最右侧显示蓝色旋转 loading icon，并提供“运行中”的无障碍文本；它与 Daemon selector 的连接 loading 分属不同语义。点击任意对话进入会话详情。超过 20 条时工作台不继续分页，用户通过底部搜索访问更早对话；项目详情仍可独立分页。

会话列表 DTO 携带 Desktop 从完整项目目录解析的项目 ID 和名称。进入详情时优先使用会话自带的项目名称，不依赖该项目是否位于工作台已加载的前 10 个项目中；只有服务端确实无法解析项目归属时才显示通用“项目”文案。

### 底部搜索

搜索框位于最近对话列表下方，不是底部导航，也不遮挡列表。聚焦或输入后进入搜索状态，在当前 Agent 范围内同时搜索项目与对话；结果分为“项目”和“对话”两个 section，不按项目重新分组对话。默认空 query 不发请求，输入采用约 300 ms debounce，服务端搜索全部可访问记录而非仅搜索已加载的 10/20 条；各 section 独立分页。

点击项目结果进入该项目的对话列表，点击对话结果进入会话详情。清空 query 恢复工作台此前的项目展开数量、对话列表和滚动位置。Hermes 完成适配后搜索对象改为 Bot + 对话。

- 初次同步：skeleton，不显示“暂无任务”。
- 在线但 Codex 无项目：说明“请先在电脑端创建一个 Codex 项目”。项目详情页即使没有对话也保留圆形“新建对话”图标入口。
- Desktop 离线：固定 banner“电脑未连接，无法读取或发送”，显示重试与故障提示；不展示正文磁盘缓存。
- Agent error：保留 selector 中其他正常 Agent；问题 Agent 显示 stable error，并建议去 Desktop 检查。

## 会话详情

### Timeline

顶部使用 72dp 的悬浮栏，返回按钮与项目对话列表页共用尺寸和位置，固定为正圆，标题和右侧操作分别使用独立胶囊；三块控件之间仅保留半透明页面底色。时间线视口延伸到顶栏背后，首屏通过可滚动顶部留白从顶栏下方开始，向上滚动后文字可透过控件间隙继续辨认。对话标题使用约 15sp 常规字重并单行省略，标题下方显示“项目名称 · Agent 名称”。连接状态保留为标题下方前导状态点及完整无障碍朗读，overflow 继续承载低频操作。内容 block：

- user/assistant：常规气泡/正文；由 `app/lib/features/conversation/markdown_renderer.dart` 使用 GFM 安全 renderer，支持标题、强调、列表、引用、表格、任务列表、代码块和选择/复制；不解析任意 HTML，不自动加载图片；`http/https` 链接显示目标 host 并在打开前二次确认。
- summary item：daemon 仅返回 `summaryKind + summaryValue` 和语言无关 fallback；Flutter 用 ARB 生成完整本地化句子，不使用气泡、Markdown 或全文展开，status 同样以本地化弱化文字/图标展示。`hookPrompt` 不渲染；`commandExecution` 只展示命令。
- plan：独立 Timeline 卡片，直接使用 daemon 返回的原始 Markdown。默认在卡片内渲染前 5 个源文件行；超过 5 行显示“查看完整计划”，点击后打开底部弹窗，以同一 Markdown renderer 渲染完整计划。大内容通过 `contentRef` 按 offset/nextOffset/hasMore 拼接；引用过期或读取失败时在弹窗内显示错误和重试入口。
- reasoning：默认显示约 14sp、紧凑行距的本地化低对比活动行，有正文时可展开查看；折叠时尾部箭头向右，展开后箭头向下；
- tool/command：默认显示与 reasoning 相同的紧凑低对比活动行，命令存在详情时可展开并使用约 13sp 的紧凑等宽字体；折叠时尾部箭头向右，展开后箭头向下；无详情 summary 不显示无效展开入口，不展示 provider 内部类型名；
- diff：文件名与增删摘要，点击进入只读 diff viewer；
- system/error：非气泡 banner，提供可恢复动作。

打开页面在首屏 snapshot 加载并完成布局后自动定位到时间线底部，展示最新内容。运行时在时间线刷新前距离底部不超过 24 logical pixels，且新增 item 或末项 streaming 内容发生变化时，布局完成后自动滚动到新的底部；如果用户已经向上阅读、刷新前不在这个范围内，则保持原滚动位置不变。距离底部超过约 96dp 时显示圆形回到底部按钮，点击后平滑滚动并在接近底部时隐藏。streaming block 原位更新，完成后固定；event gap 时顶部显示“正在重新同步”，不重复渲染已存在 block。

首屏只读取最近 20 项并正序显示；滚动到顶部使用 opaque cursor 加载更早页，按 item ID 去重，并以新增内容高度补偿滚动位置。刷新最新页时替换同 ID item、追加新 item并保留已加载旧页。Android 正常路径直接应用 sequence 连续且 syncEpoch 相同的 `conversation.delta`，以 75 ms 节奏刷新 streaming item；缺口、epoch 变化、终态或内存上限触发权威 snapshot。其他 event 约 150 ms 合并刷新；EventSink 断开时使用 2 秒 `conversation.subscribe` 轮询恢复。`action.list` 失败只显示独立 banner，不阻断正文。Plan 卡片按原始 Markdown 行边界显示前 5 行，完整内容在底部弹窗中滚动查看；普通大 block 显示 16 KiB 摘要和“查看完整内容”，查看器按 128 KiB 连续加载。引用过期时用 item 的稳定定位重新签发并从 offset 0 读取，失败则保留当前页面和弹窗重试入口。

### Composer

仅当 `online + turn.start scope + no blocking action + no plan confirmation + no write in flight` 时可输入和发送；空闲 thread 开始新 Turn，运行中 thread 默认新增 App Server 权威队列消息。运行中的 thread 仍可修改权限、模型和思考等级；这些设置作用于后续 Turn，当前正在执行的 Turn 不宣称被修改。Desktop 的运行/空闲判定只针对当前 thread，同一 Agent 的其他 thread 运行中不应禁用当前会话。Composer 是 SafeArea 内单个悬浮圆角容器：未聚焦时为单行紧凑布局，只显示加号、输入框和发送/停止按钮；输入框聚焦后展开为两行，输入框位于上方，底行依次为加号、手掌权限、右对齐的“模型简称 · 思考等级”和发送按钮。失去焦点后重新收起，权限与模型入口不在紧凑状态显示。运行中且草稿为空时右侧按钮为停止；输入文字或选中图片后切换为向上箭头，将内容加入队列。左右两侧按钮的可见尺寸为 32dp，保留 48dp 触控区域；模型简称在输入框中省略开头的“GPT-”前缀。相册一次或分次最多选择 10 张图片，按选择顺序去重并追加；已选图片在输入框内部以单行缩略图展示，每张可单独移除，超出屏幕宽度后横向滑动。

加号打开窄屏自适应 bottom sheet，包含“上传照片”和“计划模式”；计划模式选中时显示 check，Composer 的紧凑状态同时带“计划”标识。手掌按钮打开四种稳定权限：默认权限、自动审核、只读、完全访问权限。Desktop 策略不允许的项禁用并显示原因；完全访问权限选择后必须二次确认风险。点击模型/等级状态先打开所选模型支持的思考等级列表，“模型”行进入带说明和选中标记的模型列表；前三个可选模型优先展示，其余从“其他模型”进入。更换模型时保留仍受支持的等级，否则回落到模型默认等级；已废弃但当前 thread 正在使用的模型只作为不可重新选择的当前值显示。

设置按 thread 持续，已有 thread 每次修改调用 `conversation.settings.update`，成功后刷新 snapshot 确认；运行中的修改由 Desktop 写入 App Server 的后续 Turn 设置，当前正在执行的 Turn 不改变。草稿只在当前进程内保存，首次发送随 `turn.start` 一次提交。离线、等待 action、计划确认面板显示中或设置写入中时禁用设置；离线、等待 action、计划确认面板显示中或消息写入中时禁用发送。恢复连接后重新读取 catalog、snapshot 与 queue。catalog 缺少权限或加载失败时隐藏模型/权限/计划入口，显示可重试提示，但保留文字和图片发送；加号在该降级状态直接进入图片选择。弹层允许内容滚动并限制最大宽度，以适配窄屏和 200% 字体。不展示 provider 插件列表或速度选择。

### 计划确认

当且仅当 snapshot 提供 `planConfirmation` 且没有 pending Action 时，底部用非模态悬浮面板暂时代替 Composer。面板显示“是否执行此计划？”、“是，执行此计划”、分隔线，以及始终可见的“否，请说明修改内容”输入行。输入为空时尾部为“跳过”，只在本地关闭面板并恢复 Composer；输入非空时立即变为“提交”，调用 `plan.respond(decision=revise)`。确认执行调用 `decision=implement`。提交期间禁用入口并显示 loading，明确失败保留文字与面板；结果未知时先刷新 snapshot，只有权威 revision/Turn 已变化才视为受理。

跳过标识以 `daemonId + threadId + planItemId` 写入本地偏好，只保存标识符，不保存计划正文或修改意见。同一 Plan item 在刷新、重连和重新进入时不再弹出，新 Plan item 仍会触发。Action 始终优先；Action 解决后重新同步决定是否显示。Android 返回键先由系统关闭键盘，不把返回解释成跳过。

- running：保留可输入的 Composer；草稿为空时圆按钮为“停止”，草稿非空时为“发送到队列”。队列消息以输入区上方的紧凑气泡显示，右侧三点菜单提供“编辑消息”“立即引导”“取消发送”。编辑会先从权威队列删除，再把内容放回输入框并聚焦；立即引导把该项交给 App Server 立即启动；取消删除该项。包含图片且本进程已失去图片草稿的消息禁用编辑，避免静默丢附件。
- awaiting action：composer 收起，突出“先处理待办”。
- offline/syncing/revision conflict：保留草稿于当前进程内，发送按钮禁用并给出原因。
- text + image：发送前显示缩略图和移除入口；单张与整条消息的图片总大小均不得超过 48 MiB。上传阶段锁定附件编辑并按选择顺序上传，任一图片失败则不提交 Turn，保留整条草稿供用户处理或重试。

提交后的展示分三层：`正在上传` → `已提交，等待电脑确认` → 收到 snapshot/event 后 `运行中`。断线导致结果不明时，保留本地草稿副本和醒目的“提交结果未知”，不自动生成第二条消息。

### 本地发送气泡

点击发送立即捕获文字和图片快照、显示本地气泡并清空输入区。提交期间可以准备下一条草稿，但不能并发发送；回传只修改对应发送记录。气泡分别显示正在同步、正在上传（文件/分块进度）、正在发送和等待电脑确认。正式时间线按 clientMessageId 对账；旧 Desktop 仅允许按成功响应的 Turn ID 和唯一用户消息匹配，不以文字或 running 猜测。排队消息确认后进入现有队列区域。

明确失败保留气泡，可重试或编辑；编辑仅在当前草稿为空时恢复内容。结果未知只能同步确认，不能重发。记录和队列图片按电脑、Agent、会话隔离，只保存在应用进程内存，退出页面重进可恢复。首次创建会话失败保留记录；创建结果未知不自动重新创建。正文和图片不写入持久化存储。

### Interrupt

“停止”确认文案说明可能保留已产生的输出。提交后进入“正在停止”，按钮不可重复点击；若先收到 completed，则按真实 completed 展示，不强行标成 interrupted。

### 首次发送与恢复交互

第一次点击发送后先创建并同步会话，成功同步前不提交文字或上传图片。同步失败保留输入和新会话 ID；再次点击只重试同步及发送，不重复创建。正常发送与设置修改先读取权威快照，与后台事件刷新重叠时共同等待同一个读取结果，不将后台刷新视为发送同步失败。快照读取失败仍阻止写入并保留草稿；请求完成后下一次操作重新同步。发生版本冲突时显示“会话状态已变化，请重试”，刷新页面但不清空草稿、不自动发送。计划确认冲突后保留修订输入，由用户基于刷新后的计划重新选择。

`capabilities.queue=false` 的会话不显示队列编辑控件；运行时点击发送保留文字和图片，提示“此会话暂不支持消息排队，请等待当前任务结束”。队列能力字段缺失保持现有交互。电脑/Agent 切换或页面退出后的迟到响应不能修改新选择的页面。

## 待处理 Action

待处理 action 优先附着到对应对话：Desktop 在 `ConversationSummary.needsAttention` 中投影与 thread 关联的未完成用户 action，工作台/项目页在标题右侧显示橙色描边感叹号。进入会话后，Action 以底部非模态面板替换 Composer，优先级固定为 `Action > Plan confirmation > Queue + Composer`；多个 Action 按 Desktop 返回的创建时间顺序逐个展示，并提示剩余数量，处理成功后同步权威状态再展示下一项。工作台顶部存在一个或多个待处理对话时显示紧凑“n 个待处理”入口并优先进入第一条关联对话，不建立独立一级 action 页面。`attestation/generate` 等客户端基础设施请求不属于 Mobile 待办，不显示也不阻塞其他会话输入。

- command/其他 approval：保持兼容的“允许 / 拒绝”操作，不进入问题弹窗。完整命令、文件、网络上下文与 provider 的全部审批选项仍属于尚未完成的 approval DTO，当前界面不能宣称提供永久授权。
- `item/tool/requestUserInput`：仅当它是当前最早 Action 时自动打开模态弹窗，同时保留底部 Action 面板作为关闭弹窗后的再次入口；关闭弹窗不会越过当前 Action 自动打开后续问题。弹窗一次显示本 action 的全部问题；每题展示 header、问题、单选项与说明，并始终允许输入自定义答案。选择选项和自定义输入互斥，所有问题都有非空答案后底部“提交”按钮才启用；`isSecret` 问题使用遮蔽输入。若 provider 返回无法展示的空问题列表，面板只提供拒绝操作并提交协议允许的空 answers，使异常 Action 不会永久阻塞后续待办。

问题提交按 question id 组装 `answers[id].answers=[value]` 并只调用一次 `action.resolve`。普通审批和问题提交期间禁用当前面板入口并显示进度，防止重复响应；成功后刷新会话与工作台，移除提醒；失败保留 Action 面板和用户可再次打开的入口，不自动重发。`already_resolved`、action 失效、Turn 结束或 revoke 最终都以权威同步结果清理。Action 面板限制最大屏幕占比，较长内容在面板内部滚动，操作按钮保持可见；审批按钮可换行以适配动态字体。“回到底部”按钮定位在时间线区域内，自动避让 Action、Plan、Queue 和 Composer 底部区域。

## Daemon 管理与设置

Daemon selector 底部提供“添加电脑”和“管理电脑”。管理页卡片显示本地别名、Relay origin 和连接状态，电脑图标相对“别名 + 状态”两行信息整体垂直居中；每行支持拖动排序、启用/禁用及右侧更多菜单编辑名称和 Relay origin、删除电脑。删除前显示电脑名称与本机配对信息删除的确认弹窗；取消不修改状态，失败保留列表并提示重试。成功后移除本机加密 catalog 中的该条 credential 和相关选择偏好；删除当前电脑时选择排序最前的其他已启用电脑并重新同步，没有可用电脑时清空工作台内容，最后一台删除后返回配对页。删除只影响本机配对，Desktop 侧授权记录仍需在 Desktop 管理页撤销。页面不放置常驻底部栏。

设置项：

- 通知权限状态与跳转系统设置；通知文案始终通用，V1 不提供正文预览开关。
- 应用锁超时（立即/1 分钟/5 分钟/关闭）和 Android 隐私增强选项属于后续能力；当前版本不禁止系统截图、录屏或后台任务缩略图。关闭应用锁不改变 key storage。
- Debug 构建把 method、耗时、结果/error code 和 request ID 输出到 Logcat；App 内仅保留最近 100 条同样字段并支持复制。诊断不包含参数、正文、二维码、pairing credential 或 ciphertext，也不提供“导出完整流量”。

## Push 入口

- `action_required`：打开工作台并同步所有已配对 Daemon；若唯一定位到 action，则进入关联会话并展开 action，否则展示当前 Daemon 的待处理 sheet。
- `turn_completed`：打开工作台并同步；如果同步后唯一发现刚完成的 thread，可进入该详情，否则停留最近对话列表。
- `generic`：打开上次选中的 Daemon/Agent 工作台并同步。

冷启动时先经过应用锁，再处理 push intent。同步失败时保留工作台和 offline banner；绝不根据通知文字构造假的 action/thread。

## 状态与错误文案

| 稳定状态/错误 | 主文案 | 用户动作 |
| --- | --- | --- |
| connecting/handshaking/syncing | 正在连接电脑 / 正在建立安全连接 / 正在同步 | 等待，可取消或切换设备 |
| daemon offline / temporarily unavailable | 电脑未连接 | 重试、查看 Desktop 检查项 |
| conversation busy | 任务仍在运行 | 查看运行状态或停止 |
| revision conflict | 内容已更新，正在重新同步 | 自动刷新后再允许发送 |
| already resolved | 已在另一端处理 | 关闭并刷新 |
| forbidden/scope missing | 此手机未获得该权限 | 去 Desktop 重新配对授权 |
| payload too large | 图片过大，无法发送 | 重新选择或压缩 |
| response too large | 内容页仍然过大 | 刷新；展开诊断并检查 Desktop |
| content ref expired | 完整内容链接已过期 | 刷新对话后重新打开 |
| queue full / request timeout | 电脑暂时繁忙 / 请求超时 | 保留已显示内容并重试失败部分 |
| ambiguous write | 操作结果未知 | 先查看同步结果，再由用户决定 |
| revoked/key lost | 连接已失效 | 删除本地记录并重新配对 |

错误主文案只来自 Mobile 的稳定 code 映射；服务端 message 放在“详细信息”，防止泄露内部路径或形成不可本地化文案。

## 可访问性与适配

- 所有状态不仅靠颜色表达；运行、等待、失败同时有图标和文本。
- 支持系统动态字体到至少 200%、屏幕阅读器 label、逻辑焦点顺序、44/48dp 最小触控目标和 reduced motion。
- 工作台、项目列表和会话详情采用留白清晰的扁平层级：常规正文约 16sp、列表标题约 17sp、section 标题 18sp、项目与对话列表基础高度约 54dp；会话活动行约 14sp、紧凑布局高度约 32dp、行高约 1.15，详情约 13sp、行高约 1.25；动态字体放大时高度随内容增长，不以固定高度裁切文字。
- Daemon 与 Agent selector 在大字体下保持独立可访问性 label；Daemon 下拉菜单和横向 Agent 项允许名称省略并提供完整朗读。横屏/平板使用列表—详情双栏，但保持相同返回栈语义，手机窄屏使用单列。
- Markdown、diff 和 code block 支持选择/复制，但复制前对疑似 secret 不做不可靠自动脱敏承诺，使用明确隐私提示。

## 安全与失败处理

- App 切后台立即覆盖敏感 UI；重新进入按应用锁策略认证后再移除遮罩。
- 外部链接显示目标域名并二次确认；不自动加载消息中的远程图片、脚本或 iframe。
- QR、approval 命令、tool output 和 diff 不进入 analytics/crash breadcrumb。
- 配对、发送、审批、中断的 loading 都有超时后的可解释状态，不留下永久 spinner。

## 不变量与非目标

- “已连接”“已提交”“运行中”“已完成”是不同 UI 状态，只有对应权威信号才能前进。
- 不显示超出 scope 的可操作按钮，不在离线时接受会稍后发送的输入。
- App 不使用底部导航；Daemon 与 Agent 选择是工作台上下文，不是独立 tab。
- V1 优先适配 Codex；Hermes 使用 Bot 而非项目的能力模型已保留，但 Bot UI/协议尚未实现。Claude Code、OpenCode、Pi 等同样不得显示为已支持。
- V1 不提供通知内 action、不持久化草稿/正文、不支持多选批量审批；搜索只覆盖项目 metadata 与对话可搜索字段，不承诺本地全文索引。新建 thread 仅支持当前已同步的 Codex 项目，不提供独立于项目的空白 thread。

## 代码归属

当前路径：`mobile/app/lib/features/{pairing,workspace,project,conversation}/`、`mobile/app/lib/app/` 与 `mobile/app/lib/core/`。启动图标源位于 `mobile/app/branding/launcher-icon.svg`，平台图标分别位于 `mobile/app/android/app/src/main/res/mipmap-*/` 和 `mobile/app/ios/Runner/Assets.xcassets/AppIcon.appiconset/`。Markdown 正文 renderer 位于 `mobile/app/lib/features/conversation/markdown_renderer.dart`；`actions` 当前内嵌在会话页；独立 action schema 尚未实现。

可点击交互参考：[Mobile 高保真原型 V3](../../../designs/work-anywhere-mobile/index-v3.html)。原型采用白底扁平列表、胶囊上下文与单一蓝色动作色，用于确认信息层级、页面状态和交互路径，不代表 Mobile 运行代码已经实现。

## 验证方式

- 每个页面覆盖 loading、empty、online、offline、stale、error、restricted 和 revoked golden/widget tests；项目页额外验证圆形图标“新建对话”按钮、tooltip/无障碍语义、重复点击保护、创建成功跳转和创建失败提示；管理电脑页验证删除确认、取消、失败后保留列表、当前电脑切换和删除最后一台后返回配对页；Daemon 绿色/灰色状态点与两类 loading icon 都验证文本语义。
- E2E 覆盖首次配对、拒绝/过期、恢复上次 Daemon/Agent、多 Daemon/Agent 切换、项目 10 条展开、项目会话 15 条分页、最近对话 20 条、跨项目搜索、运行中蓝色 icon、流式输出、运行中消息排队及编辑/立即引导/取消、cursor gap、action race、图片失败、interrupt race 和 push cold start。
- Markdown widget 测试覆盖 GFM 常用格式、文本选择、代码块、链接确认入口和远程图片拦截；链接只允许 `http/https`，打开失败保留在当前页面并显示可解释提示。
- VoiceOver/TalkBack、200% 字体、reduced motion、窄屏/平板、深色模式和本地化伪语言人工验收。
- Android/iOS 启动图标逐档核对尺寸、白色不透明背景与黑色连线；真机启动器和主屏幕核对系统裁切后的图形。
- 用户测试重点记录：能否区分 Desktop 离线与任务失败、能否理解“等待电脑确认”、是否会在 ambiguous write 后误重复发送。

## 文档更新触发

修改启动图标、根页面、Daemon/Agent/Provider 层级、项目/Bot 差异、列表数量/分页、搜索、状态 icon、页面职责、发送/action/interrupt、错误码映射、通知入口、隐私交互、可访问性或 V1 范围时，必须更新本文档及其他受影响文档。

### 消息内嵌图片

会话消息中的 data:image/...;base64 数据由 timeline_message_body.dart 在内存解码并显示图片，保留图片前后的文字和多张图片顺序，不显示编码正文。图片数据被截断时使用现有 contentRef 分块读取完整内容，过期引用由控制器重新解析；读取失败提供重试，解码失败显示破损图片图标。完整内容弹窗使用相同渲染器。图片只驻留进程内存，显示解码限制到 1024 像素宽度；普通 Markdown 远程图片继续禁止自动加载。验证覆盖文字与多图混排及无效 base64 占位；修改解析、完整内容加载或图片展示时同步更新本节。

## 电脑选择、权限与错误恢复

启动时显示 Core catalog 的活动电脑，历史 UI 偏好不能覆盖它。全部电脑禁用时工作台无活动项；启用一台后自动选择并连接，已有有效活动项时启用其他电脑不切换。排序保存后重启保持；切换、启停、编辑或删除失败时从 Core 重新读取真实状态。

无法准确映射的线程权限显示“自定义权限 / Custom permissions”，只代表现有策略，不在新会话的内置模式菜单中作为可选预设。修改模型或计划模式保留自定义权限，由 App Server 决定是否符合配置约束。48 MiB 图片选择通过后仍可能因完整请求超过 64 MiB 收到 `too_large`，应保留文字和图片草稿，让用户减少内容后重试。
