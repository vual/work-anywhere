package io.workanywhere.work_anywhere

import android.content.Context
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.view.WindowManager
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.EventChannel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class MainActivity : FlutterActivity() {
    private lateinit var mobileCoreHost: ReflectiveMobileCore

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Keep task snapshots available on OEM launchers (notably MIUI). This
        // also clears the flag if an older Activity instance was restored with
        // FLAG_SECURE before the app was upgraded.
        window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        mobileCoreHost = ReflectiveMobileCore(this)
        MobileCoreApi.setUp(flutterEngine.dartExecutor.binaryMessenger, mobileCoreHost)
        EventChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            "work_anywhere/mobile_events",
        ).setStreamHandler(object : EventChannel.StreamHandler {
            override fun onListen(arguments: Any?, events: EventChannel.EventSink?) {
                mobileCoreHost.setEventSink(events)
            }

            override fun onCancel(arguments: Any?) {
                mobileCoreHost.setEventSink(null)
            }
        })
    }

    override fun onDestroy() {
        if (::mobileCoreHost.isInitialized) {
            mobileCoreHost.setEventSink(null)
            mobileCoreHost.close()
        }
        super.onDestroy()
    }
}

private class ReflectiveMobileCore(context: Context) : MobileCoreApi {
    private val credentials = SecureCredentialStore(context.applicationContext)
    private val core: Any? = runCatching {
        val factory = Class.forName("mobilecore.Mobilecore")
        factory.getMethod("newCore").invoke(null)
    }.getOrNull()
    private val mainHandler = Handler(Looper.getMainLooper())
    private var nativeEventSink: mobilecore.EventSink? = null

    fun setEventSink(sink: EventChannel.EventSink?) {
        val callback = sink?.let { flutterSink ->
            object : mobilecore.EventSink {
                override fun onEvent(eventJSON: String) {
                    mainHandler.post { flutterSink.success(eventJSON) }
                }
            }
        }
        nativeEventSink = callback
        core?.let { nativeCore ->
            runCatching {
                nativeCore.javaClass
                    .getMethod("setEventSink", Class.forName("mobilecore.EventSink"))
                    .invoke(nativeCore, callback)
            }
        }
    }

    override suspend fun pair(uri: String): CoreReply = withContext(Dispatchers.IO) {
        invoke("pair", arrayOf<Class<*>>(String::class.java), arrayOf<Any>(uri))
    }

    override suspend fun restoreStored(): CoreReply = withContext(Dispatchers.IO) {
        val stored = credentials.load()
            ?: return@withContext failure("no_stored_pairing", "No pairing credentials are stored")
        invoke("restore", arrayOf<Class<*>>(String::class.java), arrayOf<Any>(stored))
    }

    override fun persistCredentials(): CoreReply {
        val exported = invoke("exportCredentials", emptyArray<Class<*>>(), emptyArray<Any>())
        if (!exported.ok) return exported
        return runCatching {
            credentials.save(JSONObject(exported.payload).getString("result"))
            CoreReply(ok = true, payload = "{\"ok\":true}")
        }.getOrElse { failure("credential_store_failed", it.message ?: "Unable to store pairing credentials") }
    }

    override fun deleteStoredCredentials(): CoreReply = runCatching {
        credentials.delete()
        CoreReply(ok = true, payload = "{\"ok\":true}")
    }.getOrElse { failure("credential_delete_failed", it.message ?: "Unable to delete pairing credentials") }

    override suspend fun call(method: String, paramsJson: String, timeoutMilliseconds: Long): CoreReply = withContext(Dispatchers.IO) {
        invoke(
            "call",
            arrayOf<Class<*>>(String::class.java, String::class.java, java.lang.Long.TYPE),
            arrayOf<Any>(method, paramsJson, timeoutMilliseconds),
        )
    }

    override fun status(): CoreReply = invoke("status", emptyArray<Class<*>>(), emptyArray<Any>())

    override fun close() {
        core?.let { nativeCore ->
            runCatching { nativeCore.javaClass.getMethod("close").invoke(nativeCore) }
        }
    }

    private fun invoke(name: String, types: Array<Class<*>>, arguments: Array<Any>): CoreReply {
        val nativeCore = core
            ?: return failure("native_core_unavailable", "Mobile Go Core is not packaged in this build")
        return runCatching {
            val payload = nativeCore.javaClass.getMethod(name, *types).invoke(nativeCore, *arguments) as String
            CoreReply(ok = true, payload = payload)
        }.getOrElse { failure("native_core_failed", it.cause?.message ?: it.message ?: "Native core call failed") }
    }

    private fun failure(code: String, message: String): CoreReply {
        val payload = JSONObject()
            .put("error", JSONObject().put("code", code).put("message", message))
            .toString()
        return CoreReply(ok = false, payload = payload)
    }
}

private class SecureCredentialStore(context: Context) {
    private val preferences = context.getSharedPreferences("waw_secure_pairing", Context.MODE_PRIVATE)
    private val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    fun save(value: String) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ciphertext = cipher.doFinal(value.toByteArray(Charsets.UTF_8))
        preferences.edit()
            .putString("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .putString("ciphertext", Base64.encodeToString(ciphertext, Base64.NO_WRAP))
            .apply()
    }

    fun load(): String? {
        val iv = preferences.getString("iv", null) ?: return null
        val ciphertext = preferences.getString("ciphertext", null) ?: return null
        return runCatching {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, Base64.decode(iv, Base64.NO_WRAP)))
            String(cipher.doFinal(Base64.decode(ciphertext, Base64.NO_WRAP)), Charsets.UTF_8)
        }.getOrElse {
            runCatching { delete() }
            null
        }
    }

    fun delete() {
        preferences.edit().clear().apply()
        if (keyStore.containsAlias(KEY_ALIAS)) keyStore.deleteEntry(KEY_ALIAS)
    }

    private fun key(): SecretKey {
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    companion object {
        private const val KEY_ALIAS = "work-anywhere-mobile-pairing-v1"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
    }
}
