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
    }

    /** Flow of the TOML config string, or null if not configured. */
    val configToml: Flow<String?> = context.dataStore.data.map { prefs ->
        prefs[KEY_CONFIG_TOML]
    }

    /** Flow indicating whether a config exists. */
    val hasConfig: Flow<Boolean> = configToml.map { it != null }

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
}
