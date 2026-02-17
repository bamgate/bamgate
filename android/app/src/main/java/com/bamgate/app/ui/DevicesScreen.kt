package com.bamgate.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Circle
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.bamgate.app.data.ConfigRepository
import com.bamgate.app.ui.theme.Gray
import com.bamgate.app.ui.theme.Green
import com.bamgate.app.ui.theme.Red
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Tunnel
import org.json.JSONArray
import org.json.JSONObject

/**
 * A registered device from the server API.
 */
private data class RegisteredDevice(
    val deviceId: String,
    val deviceName: String,
    val address: String,
    val createdAt: Long,
    val lastSeenAt: Long?,
    val revoked: Boolean
)

/**
 * Immutable snapshot of a peer's advertised capabilities and current
 * server-side accepted selections (for online devices only).
 */
private data class PeerOffering(
    val peerId: String,
    val address: String,
    val state: String,
    val advertisedRoutes: List<String>,
    val advertisedDns: List<String>,
    val advertisedDnsSearch: List<String>,
    val acceptedRoutes: Set<String>,
    val acceptedDns: Set<String>,
    val acceptedDnsSearch: Set<String>
)

/**
 * Merged view of a device: server registration + optional live peer data.
 */
private data class DeviceInfo(
    val device: RegisteredDevice,
    val isThisDevice: Boolean,
    val offering: PeerOffering?
)

/**
 * Immutable local selections for a single peer. Replaced (not mutated)
 * on every checkbox toggle so Compose detects the change.
 */
private data class PeerSelections(
    val routes: Set<String>,
    val dns: Set<String>,
    val dnsSearch: Set<String>
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DevicesScreen(
    tunnel: Tunnel?,
    configRepo: ConfigRepository,
    isVpnRunning: Boolean,
    onBack: () -> Unit,
    onDisconnectVpn: () -> Unit
) {
    val scope = rememberCoroutineScope()
    var devices by remember { mutableStateOf<List<DeviceInfo>>(emptyList()) }
    var errorMessage by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(true) }

    // Local selections — keyed by peerId. Initialised from the server's
    // accepted selections when offerings are first loaded, then only
    // modified by checkbox toggles (no network calls until Apply).
    var localSelections by remember { mutableStateOf<Map<String, PeerSelections>>(emptyMap()) }
    var hasChanges by remember { mutableStateOf(false) }
    var applying by remember { mutableStateOf(false) }

    // Poll device list + peer offerings periodically, but pause while the
    // user has unsaved edits so we don't overwrite their checkbox state.
    LaunchedEffect(tunnel, hasChanges) {
        while (isActive) {
            if (tunnel != null && !hasChanges) {
                try {
                    val (deviceList, offerings) = withContext(Dispatchers.IO) {
                        val devJson = try {
                            tunnel.listDevices()
                        } catch (_: Exception) {
                            "[]"
                        }
                        val offJson = tunnel.peerOfferings
                        Pair(devJson, offJson)
                    }

                    val registered = parseDevices(deviceList)
                    val liveOfferings = parsePeerOfferings(offerings)
                        .associateBy { it.peerId }
                    val deviceId = tunnel.deviceID

                    // Merge: registered devices + live peer data.
                    val merged = registered.map { dev ->
                        DeviceInfo(
                            device = dev,
                            isThisDevice = dev.deviceId == deviceId,
                            offering = liveOfferings[dev.deviceName]
                        )
                    }
                    devices = merged

                    // Initialise local selections for new online peers only.
                    val updated = localSelections.toMutableMap()
                    for (info in merged) {
                        val off = info.offering ?: continue
                        if (off.peerId !in updated) {
                            updated[off.peerId] = PeerSelections(
                                routes = off.acceptedRoutes,
                                dns = off.acceptedDns,
                                dnsSearch = off.acceptedDnsSearch
                            )
                        }
                    }
                    // Remove peers that are no longer present.
                    val currentIds = merged.mapNotNull { it.offering?.peerId }.toSet()
                    updated.keys.retainAll(currentIds)

                    localSelections = updated
                    errorMessage = null
                    loading = false
                } catch (e: Exception) {
                    errorMessage = e.message
                    loading = false
                }
            } else if (tunnel == null) {
                loading = false
            }
            delay(3000)
        }
    }

    // Toggle a single item in a peer's local selections.
    fun toggle(peerId: String, category: String, item: String, checked: Boolean) {
        val sel = localSelections[peerId] ?: return
        val newSel = when (category) {
            "routes" -> sel.copy(
                routes = if (checked) sel.routes + item else sel.routes - item
            )
            "dns" -> sel.copy(
                dns = if (checked) sel.dns + item else sel.dns - item
            )
            "dns_search" -> sel.copy(
                dnsSearch = if (checked) sel.dnsSearch + item else sel.dnsSearch - item
            )
            else -> return
        }
        localSelections = localSelections + (peerId to newSel)

        // Recheck whether any peer has unsaved changes.
        hasChanges = devices.any { info ->
            val off = info.offering ?: return@any false
            val s = localSelections[off.peerId] ?: return@any false
            s.routes != off.acceptedRoutes ||
                s.dns != off.acceptedDns ||
                s.dnsSearch != off.acceptedDnsSearch
        }
    }

    // Apply all local selections to the Go tunnel, persist config, and reconnect.
    fun applySelections() {
        applying = true
        scope.launch {
            try {
                for (info in devices) {
                    val off = info.offering ?: continue
                    val sel = localSelections[off.peerId] ?: continue
                    val selectionsJson = JSONObject().apply {
                        put("routes", JSONArray(sel.routes.toList()))
                        put("dns", JSONArray(sel.dns.toList()))
                        put("dns_search", JSONArray(sel.dnsSearch.toList()))
                    }.toString()

                    withContext(Dispatchers.IO) {
                        tunnel?.configurePeer(off.peerId, selectionsJson)
                    }
                }

                // Persist the live tunnel's config to DataStore.
                val canonical = withContext(Dispatchers.IO) {
                    tunnel?.config ?: return@withContext null
                } ?: return@launch
                configRepo.saveConfig(canonical)

                hasChanges = false
                errorMessage = null

                // Disconnect so the VPN reconnects with new DNS/routes.
                if (isVpnRunning) {
                    onDisconnectVpn()
                    onBack()
                }
            } catch (e: Exception) {
                errorMessage = e.message
            } finally {
                applying = false
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Devices") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = "Back"
                        )
                    }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(horizontal = 16.dp)
                .verticalScroll(rememberScrollState())
        ) {
            when {
                tunnel == null -> {
                    Spacer(modifier = Modifier.height(32.dp))
                    Text(
                        text = "Connect to view devices.",
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(16.dp)
                    )
                }
                loading -> {
                    Spacer(modifier = Modifier.height(32.dp))
                    Text(
                        text = "Loading devices...",
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(16.dp)
                    )
                }
                devices.isEmpty() -> {
                    Spacer(modifier = Modifier.height(32.dp))
                    Text(
                        text = "No devices registered.",
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(16.dp)
                    )
                }
                else -> {
                    for ((index, info) in devices.withIndex()) {
                        if (index > 0) {
                            HorizontalDivider(modifier = Modifier.padding(vertical = 8.dp))
                        }

                        DeviceSection(
                            info = info,
                            selections = info.offering?.let { localSelections[it.peerId] },
                            onToggle = { category, item, checked ->
                                info.offering?.let { off ->
                                    toggle(off.peerId, category, item, checked)
                                }
                            }
                        )
                    }

                    // Apply & Reconnect button
                    if (hasChanges) {
                        Spacer(modifier = Modifier.height(24.dp))
                        Button(
                            onClick = { applySelections() },
                            enabled = !applying,
                            modifier = Modifier.fillMaxWidth()
                        ) {
                            Text(if (applying) "Applying..." else "Apply & Reconnect")
                        }
                    }
                }
            }

            // Error display
            errorMessage?.let { msg ->
                Spacer(modifier = Modifier.height(16.dp))
                Text(
                    text = "Error: $msg",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(bottom = 16.dp)
                )
            }

            Spacer(modifier = Modifier.height(16.dp))
        }
    }
}

@Composable
private fun DeviceSection(
    info: DeviceInfo,
    selections: PeerSelections?,
    onToggle: (category: String, item: String, checked: Boolean) -> Unit
) {
    val isOnline = info.isThisDevice || info.offering != null

    // Device header
    Column(modifier = Modifier.padding(vertical = 8.dp)) {
        // Name + status indicator
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically
        ) {
            // Status dot
            val dotColor = when {
                info.device.revoked -> Red
                isOnline -> Green
                else -> Gray
            }
            Icon(
                imageVector = Icons.Filled.Circle,
                contentDescription = null,
                tint = dotColor,
                modifier = Modifier.size(10.dp)
            )
            Spacer(modifier = Modifier.width(8.dp))

            // Device name
            Text(
                text = info.device.deviceName,
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.weight(1f)
            )

            // This device / status badge
            val badge = when {
                info.isThisDevice -> "this device"
                info.device.revoked -> "revoked"
                isOnline -> info.offering?.state ?: "online"
                else -> "offline"
            }
            val badgeColor = when {
                info.device.revoked -> Red
                isOnline -> Green
                else -> Gray
            }
            Text(
                text = badge,
                style = MaterialTheme.typography.bodySmall,
                color = badgeColor
            )
        }

        // Address
        Text(
            text = info.device.address,
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(start = 18.dp)
        )
    }

    // Only show capabilities for online peers (not this device, not revoked/offline).
    val offering = info.offering
    if (offering == null || info.isThisDevice) return

    val hasCapabilities = offering.advertisedRoutes.isNotEmpty() ||
            offering.advertisedDns.isNotEmpty() ||
            offering.advertisedDnsSearch.isNotEmpty()

    if (!hasCapabilities) return

    Column(modifier = Modifier.padding(start = 18.dp)) {
        // Routes
        if (offering.advertisedRoutes.isNotEmpty()) {
            CapabilitySectionHeader("Routes")
            for (route in offering.advertisedRoutes) {
                CapabilityCheckboxRow(
                    label = route,
                    checked = selections?.routes?.contains(route) == true,
                    onCheckedChange = { checked -> onToggle("routes", route, checked) }
                )
            }
        }

        // DNS Servers
        if (offering.advertisedDns.isNotEmpty()) {
            CapabilitySectionHeader("DNS Servers")
            for (dns in offering.advertisedDns) {
                CapabilityCheckboxRow(
                    label = dns,
                    checked = selections?.dns?.contains(dns) == true,
                    onCheckedChange = { checked -> onToggle("dns", dns, checked) }
                )
            }
        }

        // Search Domains
        if (offering.advertisedDnsSearch.isNotEmpty()) {
            CapabilitySectionHeader("Search Domains")
            for (domain in offering.advertisedDnsSearch) {
                CapabilityCheckboxRow(
                    label = domain,
                    checked = selections?.dnsSearch?.contains(domain) == true,
                    onCheckedChange = { checked -> onToggle("dns_search", domain, checked) }
                )
            }
        }
    }
}

@Composable
private fun CapabilitySectionHeader(title: String) {
    Text(
        text = title,
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 12.dp, bottom = 4.dp)
    )
}

@Composable
private fun CapabilityCheckboxRow(
    label: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 2.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Checkbox(
            checked = checked,
            onCheckedChange = onCheckedChange
        )
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            fontFamily = FontFamily.Monospace,
            modifier = Modifier.padding(start = 4.dp)
        )
    }
}

/**
 * Parses the JSON array returned by tunnel.listDevices() into registered devices.
 */
private fun parseDevices(json: String): List<RegisteredDevice> {
    val arr = JSONArray(json)
    val result = mutableListOf<RegisteredDevice>()

    for (i in 0 until arr.length()) {
        val obj = arr.getJSONObject(i)
        result.add(
            RegisteredDevice(
                deviceId = obj.optString("device_id", ""),
                deviceName = obj.optString("device_name", ""),
                address = obj.optString("address", ""),
                createdAt = obj.optLong("created_at", 0),
                lastSeenAt = if (obj.isNull("last_seen_at")) null
                    else obj.optLong("last_seen_at"),
                revoked = obj.optBoolean("revoked", false)
            )
        )
    }

    return result
}

/**
 * Parses the JSON array returned by tunnel.getPeerOfferings() into a list
 * of PeerOffering data objects.
 */
private fun parsePeerOfferings(json: String): List<PeerOffering> {
    val arr = JSONArray(json)
    val result = mutableListOf<PeerOffering>()

    for (i in 0 until arr.length()) {
        val obj = arr.getJSONObject(i)
        val advertised = obj.optJSONObject("advertised") ?: JSONObject()
        val accepted = obj.optJSONObject("accepted") ?: JSONObject()

        result.add(
            PeerOffering(
                peerId = obj.optString("peer_id", ""),
                address = obj.optString("address", ""),
                state = obj.optString("state", "unknown"),
                advertisedRoutes = jsonArrayToList(advertised.optJSONArray("routes")),
                advertisedDns = jsonArrayToList(advertised.optJSONArray("dns")),
                advertisedDnsSearch = jsonArrayToList(advertised.optJSONArray("dns_search")),
                acceptedRoutes = jsonArrayToList(accepted.optJSONArray("routes")).toSet(),
                acceptedDns = jsonArrayToList(accepted.optJSONArray("dns")).toSet(),
                acceptedDnsSearch = jsonArrayToList(accepted.optJSONArray("dns_search")).toSet()
            )
        )
    }

    return result
}

private fun jsonArrayToList(arr: JSONArray?): List<String> {
    if (arr == null) return emptyList()
    val list = mutableListOf<String>()
    for (i in 0 until arr.length()) {
        list.add(arr.getString(i))
    }
    return list
}
