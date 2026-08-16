pluginManagement {
    val flutterSdkPath =
        run {
            val properties = java.util.Properties()
            file("local.properties").inputStream().use { properties.load(it) }
            val flutterSdkPath = properties.getProperty("flutter.sdk")
            require(flutterSdkPath != null) { "flutter.sdk not set in local.properties" }
            flutterSdkPath
        }

    includeBuild("$flutterSdkPath/packages/flutter_tools/gradle")

    repositories {
        // Mainland mirrors come first so Android/Gradle plugin resolution does
        // not depend on direct TLS access to dl.google.com or Maven Central.
        maven("https://maven.aliyun.com/repository/google")
        maven("https://maven.aliyun.com/repository/central")
        maven("https://maven.aliyun.com/repository/gradle-plugin")
    }
}

dependencyResolutionManagement {
    // Flutter plugins often append google()/mavenCentral() themselves. Prefer
    // the settings-level mirrors so those unreachable fallbacks are ignored.
    repositoriesMode.set(RepositoriesMode.PREFER_SETTINGS)
    repositories {
        maven("https://maven.aliyun.com/repository/google")
        maven("https://maven.aliyun.com/repository/central")
        maven("https://maven.aliyun.com/repository/gradle-plugin")
        maven("https://mvn.getui.com/nexus/content/repositories/releases/")
        // The mainland mirror is materially faster for the 45 MB profile
        // engine artifact. The official repository remains the fallback; CI
        // should reject non-XML POM responses before warming a shared cache.
        maven("https://storage.flutter-io.cn/download.flutter.io")
        maven("https://storage.googleapis.com/download.flutter.io")
        maven("https://jitpack.io")
    }
}

plugins {
    id("dev.flutter.flutter-plugin-loader") version "1.0.0"
    id("com.android.application") version "9.0.1" apply false
    id("org.jetbrains.kotlin.android") version "2.3.20" apply false
}

include(":app")
