import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

val releaseSigningProperties = Properties().apply {
    val file = rootProject.file("key.properties")
    if (file.exists()) file.inputStream().use(::load)
}

fun releaseSigningValue(name: String): String? = providers.gradleProperty(name)
    .orElse(providers.environmentVariable(name))
    .orElse(releaseSigningProperties.getProperty(name, ""))
    .orNull
    ?.trim()

val releaseStoreFile = releaseSigningValue("RELEASE_STORE_FILE")
val releaseStorePassword = releaseSigningValue("RELEASE_STORE_PASSWORD")
val releaseKeyAlias = releaseSigningValue("RELEASE_KEY_ALIAS")
val releaseKeyPassword = releaseSigningValue("RELEASE_KEY_PASSWORD")
val releaseSigningValues = listOf(
    releaseStoreFile,
    releaseStorePassword,
    releaseKeyAlias,
    releaseKeyPassword,
)
val hasAnyReleaseSigningValue = releaseSigningValues.any { !it.isNullOrEmpty() }
val hasReleaseSigning = releaseSigningValues.all { !it.isNullOrEmpty() }

if (hasAnyReleaseSigningValue && !hasReleaseSigning) {
    throw GradleException(
        "Incomplete release signing configuration. Provide RELEASE_STORE_FILE, " +
            "RELEASE_STORE_PASSWORD, RELEASE_KEY_ALIAS and RELEASE_KEY_PASSWORD.",
    )
}

android {
    val localProperties = Properties().apply {
        val file = rootProject.file("local.properties")
        if (file.exists()) file.inputStream().use(::load)
    }
    namespace = "com.qingwaguagua.imapp"
    // API 36 is required transitively by the pinned LiveKit 2.7.0 graph.
    // This does not opt the app into new target-SDK runtime behavior.
    compileSdk = 36
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        applicationId = "com.qingwaguagua.imapp"
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
        manifestPlaceholders["GETUI_APPID"] =
            providers.gradleProperty("GETUI_APPID")
                .orElse(providers.environmentVariable("GETUI_APPID"))
                .orElse(localProperties.getProperty("GETUI_APPID", ""))
                .get()
    }

    signingConfigs {
        if (hasReleaseSigning) {
            create("release") {
                storeFile = rootProject.file(releaseStoreFile!!)
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
                enableV1Signing = true
                enableV2Signing = true
                enableV3Signing = true
                enableV4Signing = true
            }
        }
    }

    buildTypes {
        release {
            if (hasReleaseSigning) {
                signingConfig = signingConfigs.getByName("release")
            }
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }
}

val releaseTaskRequested = gradle.startParameter.taskNames.any { taskName ->
    taskName.contains("release", ignoreCase = true) &&
        (taskName.contains("assemble", ignoreCase = true) ||
            taskName.contains("bundle", ignoreCase = true) ||
            taskName.contains("package", ignoreCase = true))
}
if (releaseTaskRequested && !hasReleaseSigning) {
    throw GradleException(
        "Release signing is required. Configure the four RELEASE_* signing values; " +
            "debug keys must never be used for a formal release.",
    )
}

dependencies {
    implementation("com.getui:gtsdk:3.3.12.0")
    implementation("com.getui:gtc:3.2.18.0")
    testImplementation("junit:junit:4.13.2")
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}
