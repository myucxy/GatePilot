# 开发环境约定

## 1. 本机工具目录

本项目默认使用 `D:\Dev\Env` 作为本机开发工具根目录。

当前已识别工具：

- Go: `D:\Dev\Env\Go\bin\go.exe`
- Android SDK: `D:\Dev\Env\Android\Sdk`
- Flutter: `D:\Dev\Env\Flutter\flutter`
- JDK 21: `D:\Dev\Env\Jdk\jdk-21`
- Gradle: `D:\Dev\Env\gradle`
- Node.js: `D:\Dev\Env\nodejs`

如果后续需要安装 Flutter、额外 Android command line tools、NATS、Kafka 或其他开发工具，也应安装到 `D:\Dev\Env` 下，避免散落到系统盘或用户目录。

## 2. 推荐环境变量

PowerShell 本地开发可按需设置：

```powershell
$env:GOROOT = "D:\Dev\Env\Go"
$env:JAVA_HOME = "D:\Dev\Env\Jdk\jdk-21"
$env:ANDROID_HOME = "D:\Dev\Env\Android\Sdk"
$env:ANDROID_SDK_ROOT = "D:\Dev\Env\Android\Sdk"
$env:Path = "D:\Dev\Env\Go\bin;D:\Dev\Env\nodejs;D:\Dev\Env\Flutter\flutter\bin;D:\Dev\Env\Jdk\jdk-21\bin;$env:Path"
```

## 3. Android 版本要求

安卓版最低支持 Android 9，对应 API level 28。

配置位置：

```text
mobile/android/app/build.gradle
```

关键配置：

```gradle
minSdk 28
```

## 4. 当前本机限制

Flutter SDK 已按标准目录放置：

```text
D:\Dev\Env\Flutter\flutter
```

如果网络访问较慢，可以在 PowerShell 中设置代理：

```powershell
$env:http_proxy = "http://127.0.0.1:7890"
$env:https_proxy = "http://127.0.0.1:7890"
```

Flutter/Dart 包下载可使用：

```powershell
$env:PUB_HOSTED_URL = "https://pub.flutter-io.cn"
$env:FLUTTER_STORAGE_BASE_URL = "https://storage.flutter-io.cn"
```
