plugins {
    id("com.android.application")
    id("kotlin-android")
    // Flutter Gradle 插件必须在 Android 和 Kotlin 插件之后应用。
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "com.gatepilot.gatepilot_mobile"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_17.toString()
    }

    defaultConfig {
        applicationId = "com.gatepilot.gatepilot_mobile"
        // Android 9 对应 API 28，作为 GatePilot 移动端最低支持版本。
        minSdk = 28
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    buildTypes {
        release {
            // M0 阶段使用 debug 签名占位，正式发布时替换为受控签名配置。
            signingConfig = signingConfigs.getByName("debug")
        }
    }
}

flutter {
    source = "../.."
}
