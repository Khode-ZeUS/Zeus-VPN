plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.zeusvpn.app"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.zeusvpn.app"
        minSdk = 24
        targetSdk = 35
        versionCode = 11003
        versionName = "1.1.0.3"
    }

    // A release keystore can be supplied through the environment (CI
    // secrets); otherwise release builds are signed with the debug key so
    // the APK can still be installed directly.
    val keystore = System.getenv("ZEUS_KEYSTORE")
    signingConfigs {
        if (keystore != null) {
            create("release") {
                storeFile = file(keystore)
                storePassword = System.getenv("ZEUS_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ZEUS_KEY_ALIAS")
                keyPassword = System.getenv("ZEUS_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }

    // One APK per CPU type, plus a universal one.
    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a", "armeabi-v7a", "x86_64")
            isUniversalApk = true
        }
    }

    // The UI is the desktop app's frontend, served from the APK's assets.
    sourceSets["main"].assets.srcDirs("../../frontend/dist")

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    packaging {
        jniLibs.useLegacyPackaging = true
    }
    lint {
        abortOnError = false
    }
}

dependencies {
    // Built from ../mobile with gomobile (see README).
    implementation(files("libs/zeuscore.aar"))
    implementation("androidx.webkit:webkit:1.12.1")
}
