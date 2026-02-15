package com.bamgate.app.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "bamgate_config")

class ConfigRepository(private val context: Context) {

    companion object {
        private val KEY_CONFIG_TOML = stringPreferencesKey("config_toml")
        private val KEY_PENDING_INVITE_SERVER = stringPreferencesKey("pending_invite_server")
        private val KEY_PENDING_INVITE_CODE = stringPreferencesKey("pending_invite_code")
    }

    /** Flow of the TOML config string, or null if not configured. */
    val configToml: Flow<String?> = context.dataStore.data.map { prefs ->
        prefs[KEY_CONFIG_TOML]
    }

    /** Flow indicating whether a config exists. */
    val hasConfig: Flow<Boolean> = configToml.map { it != null }

    /** Flow of pending invite server (from QR deep link). */
    val pendingInviteServer: Flow<String?> = context.dataStore.data.map { prefs ->
        prefs[KEY_PENDING_INVITE_SERVER]
    }

    /** Flow of pending invite code (from QR deep link). */
    val pendingInviteCode: Flow<String?> = context.dataStore.data.map { prefs ->
        prefs[KEY_PENDING_INVITE_CODE]
    }

    /** Save the TOML config string. */
    suspend fun saveConfig(toml: String) {
        context.dataStore.edit { prefs ->
            prefs[KEY_CONFIG_TOML] = toml
        }
    }

    /** Clear the config (for testing/reset). */
    suspend fun clearConfig() {
        context.dataStore.edit { prefs ->
            prefs.remove(KEY_CONFIG_TOML)
        }
    }

    /** Store invite parameters from a QR deep link for the setup screen. */
    fun setPendingInvite(server: String, code: String) {
        // Use SharedPreferences for synchronous write from the main thread.
        // The setup screen will read these and clear them.
        context.getSharedPreferences("bamgate_invite", Context.MODE_PRIVATE)
            .edit()
            .putString("server", server)
            .putString("code", code)
            .apply()
    }

    /** Get and clear the pending invite parameters. */
    fun consumePendingInvite(): Pair<String, String>? {
        val prefs = context.getSharedPreferences("bamgate_invite", Context.MODE_PRIVATE)
        val server = prefs.getString("server", null) ?: return null
        val code = prefs.getString("code", null) ?: return null
        prefs.edit().clear().apply()
        return Pair(server, code)
    }
}
