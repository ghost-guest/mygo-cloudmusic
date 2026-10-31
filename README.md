# 网易云音乐 · MyGo（二开版）

用 Go 和 [MyGo](https://github.com/egoist/mygo) 写的第三方网易云音乐桌面客户端，原生界面，无 WebView。

本仓库是 [egoist/mygo-cloudmusic](https://github.com/egoist/mygo-cloudmusic) 的 fork，在 0.1.22 的基础上做二开：补上音乐源、VIP 与灰歌的播放、咪咕音源，以及歌单推荐与歌单搜索。界面与其余功能仍是上游原样。

## 出处

- 上游：[egoist/mygo-cloudmusic](https://github.com/egoist/mygo-cloudmusic)（作者 [egoist](https://github.com/egoist)），本项目是其 fork
- 二开基线：上游 `v0.1.22`（`d726550`）
- 本仓库：[ghost-guest/mygo-cloudmusic](https://github.com/ghost-guest/mygo-cloudmusic)
- 安装包：见 [Releases](https://github.com/ghost-guest/mygo-cloudmusic/releases)

上游后续的 0.1.23、0.1.24 尚未并入本仓库。

## 二开新增

### 1. 音乐源（LX 音源）

LX Music 生态里的音源脚本（`.js`）可直接导入使用，脚本在 [goja](https://github.com/dop251/goja) 里运行，与 LX Music 桌面版的音源格式一致。

- 设置 → 音乐源 → 「导入音乐源…」，粘贴音源链接或本地脚本即可
- 内置 5 个音源并**在首次启动时自动导入**（脚本内嵌在程序里，不需要联网）：`jiexiang`（杰翔聚合音源）、`helloworld`、`hyw`（HYWmusic 公益版）、`fish`（fish_music）、`qdy`（全豆要聚合音源），其中杰翔排在最前，作为 VIP 与灰歌的第一优先
- 音源开关、重载、移除都在设置里；删掉的默认音源不会被重新塞回来；手动导入过的同一脚本也不会被再添加一份
- 脚本可从社区仓库获取，例如 [pdone/lx-music-source](https://github.com/pdone/lx-music-source)（内置的 5 个就来自这里）

### 2. VIP 与灰歌：无感解锁

播放一首在网易云听不了的歌时，按 **酷我 → 咪咕 → QQ音乐** 的顺序自动取整曲，播放器界面上没有额外步骤，点了就放。

- 取到的整曲会按原始音质缓存到本机，第二次播放不再等待，也不耗流量
- 取链会校验整曲时长与码流，接口返回的试听片段不会被当成整曲播出
- 找到资源的歌**不再标灰或标 VIP**，与普通歌曲显示一致
- 三条链路都拿不到的歌才保持原样

### 3. 咪咕音乐原生音源

同步 [guohuiyuan/music-lib](https://github.com/guohuiyuan/music-lib) 的咪咕能力，搜索框下的「来源」下拉变成四项：**网易云 / QQ音乐 / 咪咕音乐 / 酷我**，选中即搜、点即播。

咪咕是一路独立的曲库，而且不少歌只有它给出 **FLAC 无损整曲**（如《起风了》《富士山下》《海阔天空》）。第三方平台的搜索排序普遍较差，跨平台结果都经过匹配层重新排序，避免「搜七里香播我的地盘」。

### 4. 歌单推荐与歌单搜索

- 首页「主题歌单」：90后经典老歌、90后流行歌、千禧年代金曲、粤语经典老歌、华语怀旧金曲、民谣，切换即换一批
- 搜索页新增「歌单」标签，可直接搜「90后流行歌」这类歌单，滚动翻页
- 修复了歌单搜索翻页后结果被清空、总数在 681 与 277 之间来回跳的问题

### 关于体积

相对上游，安装包大约多 1～2.5 MB（macOS arm64 dmg 9.5 MB，上游 8.1 MB）。增量主要是运行音源脚本的 JS 引擎与其依赖，去掉它就没有「导入音乐源」了。

## 上游原有功能

- 扫码登录，登录态保存在系统凭据存储中
- 每日推荐、推荐歌单、排行榜、我的歌单
- 搜索单曲、歌手、专辑、用户与播客，输入时显示搜索建议，保留搜索历史
- 歌手页的热门与最新歌曲、专辑与动态，关注歌手；专辑页与用户页
- 播客：浏览与收听节目，订阅播客；在本机把节目转写成字幕（Qwen3-ASR），像歌词一样随播放滚动
- 私人 FM（推荐 / 熟悉 / 探索）与心动模式
- 边下边播，支持标准 / 较高 / 极高 / 无损（FLAC）/ Hi-Res 音质
- 听过的歌曲缓存在本机，再次播放无需等待
- 音量均衡，暂停与继续时淡出淡入，拖动进度无杂音
- 退出后再打开，从上次的播放队列与进度继续
- 逐字歌词与翻译，正在播放页的「律动」视图与歌曲评论
- 刘海播放器：在屏幕顶部刘海处显示迷你播放器，悬停展开（在设置中开启）
- 喜欢歌曲、播放队列、随机与循环播放
- 浅色 / 深色 / 跟随系统

## 安装

| 平台 | 文件 |
| --- | --- |
| macOS Apple 芯片 | `mygo-cloudmusic-<版本>-arm64.dmg` |
| macOS Intel | `mygo-cloudmusic-<版本>-amd64.dmg` |
| Windows x64 | `mygo-cloudmusic-<版本>-windows-amd64.zip`，解压后直接运行 `MyGo 云音乐.exe` |

macOS 需要 13 及以上。安装包是 ad-hoc 签名、未公证，首次打开若提示「已损坏」：

```bash
xattr -dr com.apple.quarantine "/Applications/MyGo 云音乐.app"
```

## 构建

需要 Go 1.27+。

```bash
go tool mygo dev                  # 开发模式运行
go tool mygo build                # 打包当前平台到 build/
go test ./internal/...            # 运行测试
```

交叉打包多个平台（在 macOS 上可直接打出 Windows 版）：

```bash
go tool mygo build -platform "darwin/arm64,darwin/amd64,windows/amd64" -sign - -skip-notarize
```

## 跟进上游

```bash
git remote add upstream https://github.com/egoist/mygo-cloudmusic.git
git fetch upstream
git rebase upstream/main
```

## 致谢

- [egoist/mygo-cloudmusic](https://github.com/egoist/mygo-cloudmusic)：本项目的上游，二开的基础
- [MyGo](https://github.com/egoist/mygo)：原生 UI 与应用框架
- [guohuiyuan/music-lib](https://github.com/guohuiyuan/music-lib)：咪咕等平台的接口实现，来自 [guohuiyuan/go-music-dl](https://github.com/guohuiyuan/go-music-dl)
- [LX Music](https://github.com/lyswhut/lx-music-desktop) 与其音源社区：音源脚本格式与内置脚本的作者们（署名见各脚本头部）
- [NeteaseCloudMusicApi](https://github.com/Binaryify/NeteaseCloudMusicApi)：eapi 加密与登录流程的参考
- [folia-major](https://github.com/chthollyphile/folia-major)：歌词动画效果的参考
- [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx)、[Qwen3-ASR](https://github.com/QwenLM/Qwen3-ASR) 与 [Silero VAD](https://github.com/snakers4/silero-vad)：本机语音识别

本项目与网易云音乐官方无关，音乐版权归各权利人所有。

## 许可证

[AGPL-3.0](LICENSE)。上游与本项目同为 AGPL-3.0，二次分发请同样以 AGPL-3.0 开源。
