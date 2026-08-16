buildscript {
    repositories {
        maven("https://maven.aliyun.com/repository/google")
        maven("https://maven.aliyun.com/repository/central")
        maven("https://maven.aliyun.com/repository/gradle-plugin")
    }
}

allprojects {
    // Some Flutter plugins still declare a legacy `buildscript` classpath.
    // Inject the same mirrors before their own google()/mavenCentral() entries.
    buildscript.repositories {
        maven("https://maven.aliyun.com/repository/google")
        maven("https://maven.aliyun.com/repository/central")
        maven("https://maven.aliyun.com/repository/gradle-plugin")
    }
    repositories {
        maven("https://maven.aliyun.com/repository/google")
        maven("https://maven.aliyun.com/repository/central")
        maven { url = uri("https://mvn.getui.com/nexus/content/repositories/releases/") }
    }
}

val newBuildDir: Directory =
    rootProject.layout.buildDirectory
        .dir("../../build")
        .get()
rootProject.layout.buildDirectory.value(newBuildDir)

subprojects {
    val newSubprojectBuildDir: Directory = newBuildDir.dir(project.name)
    project.layout.buildDirectory.value(newSubprojectBuildDir)

    // livekit_client 2.7.0 pins compileSdk=34 in its Android library while
    // that same release requires connectivity_plus 7, device_info_plus 12.3
    // and flutter_webrtc 1.4, whose AAR metadata requires API 36. Keep the
    // locked upstream versions and raise only the compile API for every
    // Android library; targetSdk/minSdk and runtime behavior stay unchanged.
    afterEvaluate {
        if (plugins.hasPlugin("com.android.library")) {
            extensions.configure<com.android.build.api.dsl.LibraryExtension> {
                // Apply after each plugin's own build.gradle so hard-coded
                // values such as LiveKit's 34 cannot overwrite this gate.
                compileSdk = 36
            }
        }
    }
}
subprojects {
    project.evaluationDependsOn(":app")
}

tasks.register<Delete>("clean") {
    delete(rootProject.layout.buildDirectory)
}
