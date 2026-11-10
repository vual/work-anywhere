package io.workanywhere.work_anywhere

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageInfo
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.content.pm.Signature
import android.net.Uri
import android.os.Build
import android.provider.Settings
import org.json.JSONObject
import java.io.File
import java.security.MessageDigest

/** APK verified by Go Core (size + SHA-256) and handed to the host for install. */
data class UpdateArtifact(
    val path: String,
    val versionCode: Long,
    val packageName: String,
    val signingCertSha256: String,
)

/** Carries app-update events from the install receiver to the Flutter event sink. */
object UpdateEvents {
    @Volatile
    var listener: ((String) -> Unit)? = null

    fun emit(state: String, code: String? = null, message: String? = null) {
        val event = JSONObject().put("type", "update").put("state", state)
        if (code != null) {
            event.put("error", JSONObject().put("code", code).put("message", message ?: code))
        }
        listener?.invoke(event.toString())
    }
}

/** Android-specific half of app updates: permission, signer check and PackageInstaller. */
class UpdateInstaller(private val context: Context) {
    private val packageManager: PackageManager = context.packageManager

    fun installedPackage(): PackageInfo = packageManager.getPackageInfo(context.packageName, PackageManager.GET_SIGNING_CERTIFICATES)

    fun installedVersionCode(): Long = installedPackage().longVersionCode

    /** Release builds only: debug-signed builds can never be upgraded by a release APK. */
    fun isUpdatable(): Boolean = (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) == 0

    fun appVersion(): JSONObject {
        val info = installedPackage()
        return JSONObject()
            .put("versionName", info.versionName ?: "")
            .put("versionCode", info.longVersionCode)
            .put("packageName", context.packageName)
            .put("updatable", isUpdatable())
    }

    fun cacheDir(): File = context.cacheDir

    fun canInstallPackages(): Boolean = packageManager.canRequestPackageInstalls()

    fun openInstallPermissionSettings() {
        val intent = Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}"))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        context.startActivity(intent)
    }

    /** Deletes cache/updates/<versionCode> entries that are not newer than the installed build. */
    fun pruneInstalled() {
        val installed = runCatching { installedVersionCode() }.getOrNull() ?: return
        File(context.cacheDir, "updates").listFiles()?.forEach { entry ->
            val code = entry.name.toLongOrNull()
            if (code == null || code <= installed) entry.deleteRecursively()
        }
    }

    /**
     * Re-checks the APK independently of Go Core and submits a PackageInstaller
     * session. Returns null on submit, or a stable error code and message.
     */
    fun install(artifact: UpdateArtifact): Pair<String, String>? {
        if (!isUpdatable()) return "update_unsupported_build" to "Debug builds cannot install release updates"
        if (!canInstallPackages()) return "update_permission_required" to "Allow installing unknown apps for Work Anywhere"
        val apk = File(artifact.path)
        if (!apk.isFile || !apk.canonicalPath.startsWith(File(context.cacheDir, "updates").canonicalPath + File.separator)) {
            return "update_install_failed" to "Downloaded APK is missing"
        }
        verifySigner(apk, artifact)?.let { return "update_signer_mismatch" to it }
        return runCatching {
            submit(apk)
            null
        }.getOrElse { "update_install_failed" to (it.message ?: "Unable to start the installer") }
    }

    private fun verifySigner(apk: File, artifact: UpdateArtifact): String? {
        val archive = packageManager.getPackageArchiveInfo(apk.path, PackageManager.GET_SIGNING_CERTIFICATES)
            ?: return "APK cannot be parsed"
        val installed = installedPackage()
        if (archive.packageName != context.packageName || artifact.packageName != context.packageName) {
            return "APK belongs to another package"
        }
        if (archive.longVersionCode != artifact.versionCode || archive.longVersionCode <= installed.longVersionCode) {
            return "APK version does not match the release"
        }
        val archiveSigners = currentSigners(archive) ?: return "APK is not signed"
        val installedSigners = currentSigners(installed) ?: return "Installed app signature is unavailable"
        if (archiveSigners != installedSigners) return "APK is signed by a different certificate"
        if (artifact.signingCertSha256.lowercase() !in archiveSigners) return "APK certificate does not match the manifest"
        return null
    }

    private fun currentSigners(info: PackageInfo): Set<String>? {
        val signing = info.signingInfo ?: return null
        val signatures: Array<Signature> = if (signing.hasMultipleSigners()) {
            signing.apkContentsSigners
        } else {
            // History is ordered oldest first; the last entry is the current signer.
            signing.signingCertificateHistory?.takeLast(1)?.toTypedArray() ?: return null
        }
        if (signatures.isEmpty()) return null
        return signatures.map { sha256(it.toByteArray()) }.toSet()
    }

    private fun submit(apk: File) {
        val installer = packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
            setSize(apk.length())
        }
        val sessionId = installer.createSession(params)
        installer.openSession(sessionId).use { session ->
            apk.inputStream().use { input ->
                session.openWrite("base.apk", 0, apk.length()).use { output ->
                    input.copyTo(output)
                    session.fsync(output)
                }
            }
            val callback = Intent(context, UpdateInstallReceiver::class.java).setPackage(context.packageName)
            var flags = PendingIntent.FLAG_UPDATE_CURRENT
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) flags = flags or PendingIntent.FLAG_MUTABLE
            val pending = PendingIntent.getBroadcast(context, sessionId, callback, flags)
            session.commit(pending.intentSender)
        }
        UpdateEvents.emit("installing")
    }

    private fun sha256(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
}

/** Not exported: receives the PackageInstaller session result. */
class UpdateInstallReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        when (val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                val confirm = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                } else {
                    @Suppress("DEPRECATION")
                    intent.getParcelableExtra(Intent.EXTRA_INTENT)
                }
                if (confirm == null) {
                    UpdateEvents.emit("error", "update_install_failed", "Installer confirmation is unavailable")
                    return
                }
                confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                runCatching { context.startActivity(confirm) }
                    .onFailure { UpdateEvents.emit("error", "update_install_failed", it.message) }
            }
            PackageInstaller.STATUS_SUCCESS -> UpdateEvents.emit("installed")
            PackageInstaller.STATUS_FAILURE_ABORTED ->
                UpdateEvents.emit("error", "update_install_cancelled", "Installation was cancelled")
            PackageInstaller.STATUS_FAILURE_INCOMPATIBLE, PackageInstaller.STATUS_FAILURE_CONFLICT ->
                UpdateEvents.emit("error", "update_signer_mismatch", intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE))
            else -> UpdateEvents.emit(
                "error",
                "update_install_failed",
                intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: "Installation failed ($status)",
            )
        }
    }
}
