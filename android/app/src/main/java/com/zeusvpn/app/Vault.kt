package com.zeusvpn.app

import android.content.Context
import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The account-file key, sealed inside Android Keystore: a random 32-byte key
 * wraps the account file, and the Keystore key (which never leaves the
 * device's secure hardware) wraps IT in shared preferences. The Go engine
 * receives the raw key once at startup, in memory only — the plaintext key
 * file of the data directory is gone.
 */
object Vault {
    private const val ALIAS = "zeus_vault"
    private const val PREFS = "zeus_vault"
    private const val WRAPPED = "wrapped_key"

    /** Returns the master key as hex, creating and wrapping it if needed. */
    fun keyHex(ctx: Context): String {
        val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val km = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        if (!km.containsAlias(ALIAS)) {
            val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
            gen.init(
                KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setKeySize(256)
                    .build()
            )
            gen.generateKey()
        }
        val key = key(prefs, km)
        return key.joinToString("") { "%02x".format(it) }
    }

    // The stored key, unwrapping it with the Keystore when needed.
    private fun key(prefs: SharedPreferences, km: KeyStore): ByteArray {
        val stored = prefs.getString(WRAPPED, null)
        if (stored != null) {
            runCatching { return unwrap(km, stored) }
        }
        val key = ByteArray(32).also { SecureRandom().nextBytes(it) }
        prefs.edit().putString(WRAPPED, wrap(km, key)).apply()
        return key
    }

    private fun cipher(km: KeyStore, mode: Int): Cipher {
        val entry = km.getEntry(ALIAS) as KeyStore.SecretKeyEntry
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(mode, entry.secretKey)
        return c
    }

    private fun wrap(km: KeyStore, key: ByteArray): String {
        val c = cipher(km, Cipher.ENCRYPT_MODE)
        val out = c.doFinal(key)
        val iv = c.iv
        return java.util.Base64.getEncoder().encodeToString(iv + out)
    }

    private fun unwrap(km: KeyStore, stored: String): ByteArray {
        val blob = java.util.Base64.getDecoder().decode(stored)
        val c = cipher(km, Cipher.DECRYPT_MODE)
        c.updateAAD(ByteArray(0))
        return c.doFinal(blob.copyOfRange(12, blob.size))
    }
}
