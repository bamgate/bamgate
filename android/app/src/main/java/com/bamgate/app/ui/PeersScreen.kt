package com.bamgate.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
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
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile
import mobile.Tunnel
import org.json.JSONArray
import org.json.JSONObject

/**
 * Data class representing a peer's advertised capabilities and the user's
 * current selections, parsed from the JSON returned by tunnel.getPeerOfferings().
 */
private data class PeerOffering(
    val peerId: String,
    val address: String,
    val state: String,
    val advertisedRoutes: List<String>,
    val advertisedDns: List<String>,
    val advertisedDnsSearch: List<String>,
    val acceptedRoutes: MutableList<String>,
    val acceptedDns: MutableList<String>,
    val acceptedDnsSearch: MutableList<String>
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeersScreen(
    tunnel: Tunnel?,
    configRepo: ConfigRepository,
    isVpnRunning: Boolean,
    onBack: () -> Unit,
    onDisconnectVpn: () -> Unit
) {
    val scope = rememberCoroutineScope()
    var offerings by remember { mutableStateOf<List<PeerOffering>>(emptyList()) }
    var errorMessage by remember { mutableStateOf<String?>(null) }

    // Poll peer offerings periodically (peers can connect/disconnect).
    LaunchedEffect(tunnel) {
        while (isActive) {
            if (tunnel != null) {
                try {
                    val json = withContext(Dispatchers.IO) {
                        tunnel.peerOfferings
                    }
                    offerings = parsePeerOfferings(json)
                    errorMessage = null
                } catch (e: Exception) {
                    errorMessage = e.message
                }
            } else {
                offerings = emptyList()
            }
            delay(3000)
        }
    }

    // Saves the current selections for a peer to the Go tunnel and persists config.
    fun saveSelections(peer: PeerOffering) {
        scope.launch {
            try {
                val selectionsJson = JSONObject().apply {
                    put("routes", JSONArray(peer.acceptedRoutes))
                    put("dns", JSONArray(peer.acceptedDns))
                    put("dns_search", JSONArray(peer.acceptedDnsSearch))
                }.toString()

                withContext(Dispatchers.IO) {
                    tunnel?.configurePeer(peer.peerId, selectionsJson)
                }

                // Persist the updated config.
                val currentToml = configRepo.configToml.first() ?: return@launch
                val tempTunnel = withContext(Dispatchers.IO) {
                    Mobile.newTunnel(currentToml)
                }
                val canonical = withContext(Dispatchers.IO) {
                    tempTunnel.updateConfig(currentToml)
                }
                configRepo.saveConfig(canonical)

                // Disconnect so the VPN reconnects with new DNS/routes.
                if (isVpnRunning) {
                    onDisconnectVpn()
                }
            } catch (e: Exception) {
                errorMessage = e.message
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Peers") },
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
            if (tunnel == null) {
                Spacer(modifier = Modifier.height(32.dp))
                Text(
                    text = "Connect to view peer capabilities.",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(16.dp)
                )
            } else if (offerings.isEmpty()) {
                Spacer(modifier = Modifier.height(32.dp))
                Text(
                    text = "No peers connected.",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(16.dp)
                )
            } else {
                for ((index, peer) in offerings.withIndex()) {
                    if (index > 0) {
                        HorizontalDivider(modifier = Modifier.padding(vertical = 12.dp))
                    }

                    PeerSection(
                        peer = peer,
                        onSelectionChanged = { saveSelections(peer) }
                    )
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
private fun PeerSection(
    peer: PeerOffering,
    onSelectionChanged: () -> Unit
) {
    // Peer header
    Column(modifier = Modifier.padding(vertical = 8.dp)) {
        Text(
            text = peer.peerId,
            style = MaterialTheme.typography.titleMedium
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text(
                text = peer.address,
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            Text(
                text = peer.state,
                style = MaterialTheme.typography.bodySmall,
                color = if (peer.state == "connected" || peer.state == "completed") {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                }
            )
        }
    }

    val hasCapabilities = peer.advertisedRoutes.isNotEmpty() ||
            peer.advertisedDns.isNotEmpty() ||
            peer.advertisedDnsSearch.isNotEmpty()

    if (!hasCapabilities) {
        Text(
            text = "No capabilities advertised.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(vertical = 4.dp)
        )
        return
    }

    // Routes
    if (peer.advertisedRoutes.isNotEmpty()) {
        SectionHeader("Routes")
        for (route in peer.advertisedRoutes) {
            CheckboxRow(
                label = route,
                checked = route in peer.acceptedRoutes,
                onCheckedChange = { checked ->
                    if (checked) peer.acceptedRoutes.add(route)
                    else peer.acceptedRoutes.remove(route)
                    onSelectionChanged()
                }
            )
        }
    }

    // DNS Servers
    if (peer.advertisedDns.isNotEmpty()) {
        SectionHeader("DNS Servers")
        for (dns in peer.advertisedDns) {
            CheckboxRow(
                label = dns,
                checked = dns in peer.acceptedDns,
                onCheckedChange = { checked ->
                    if (checked) peer.acceptedDns.add(dns)
                    else peer.acceptedDns.remove(dns)
                    onSelectionChanged()
                }
            )
        }
    }

    // Search Domains
    if (peer.advertisedDnsSearch.isNotEmpty()) {
        SectionHeader("Search Domains")
        for (domain in peer.advertisedDnsSearch) {
            CheckboxRow(
                label = domain,
                checked = domain in peer.acceptedDnsSearch,
                onCheckedChange = { checked ->
                    if (checked) peer.acceptedDnsSearch.add(domain)
                    else peer.acceptedDnsSearch.remove(domain)
                    onSelectionChanged()
                }
            )
        }
    }
}

@Composable
private fun SectionHeader(title: String) {
    Text(
        text = title,
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 12.dp, bottom = 4.dp)
    )
}

@Composable
private fun CheckboxRow(
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
                acceptedRoutes = jsonArrayToList(accepted.optJSONArray("routes")).toMutableList(),
                acceptedDns = jsonArrayToList(accepted.optJSONArray("dns")).toMutableList(),
                acceptedDnsSearch = jsonArrayToList(accepted.optJSONArray("dns_search")).toMutableList()
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
