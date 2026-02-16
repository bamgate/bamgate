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
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Tunnel
import org.json.JSONArray
import org.json.JSONObject

/**
 * Immutable snapshot of a peer's advertised capabilities and current
 * server-side accepted selections.
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

    // Local selections — keyed by peerId. Initialised from the server's
    // accepted selections when offerings are first loaded, then only
    // modified by checkbox toggles (no network calls until Apply).
    var localSelections by remember { mutableStateOf<Map<String, PeerSelections>>(emptyMap()) }
    var hasChanges by remember { mutableStateOf(false) }
    var applying by remember { mutableStateOf(false) }

    // Poll peer offerings periodically, but pause while the user has
    // unsaved edits so we don't overwrite their checkbox state.
    LaunchedEffect(tunnel, hasChanges) {
        while (isActive) {
            if (tunnel != null && !hasChanges) {
                try {
                    val json = withContext(Dispatchers.IO) {
                        tunnel.peerOfferings
                    }
                    val parsed = parsePeerOfferings(json).sortedBy { it.peerId }
                    offerings = parsed

                    // Initialise local selections for new peers only,
                    // preserving existing entries.
                    val updated = localSelections.toMutableMap()
                    for (peer in parsed) {
                        if (peer.peerId !in updated) {
                            updated[peer.peerId] = PeerSelections(
                                routes = peer.acceptedRoutes,
                                dns = peer.acceptedDns,
                                dnsSearch = peer.acceptedDnsSearch
                            )
                        }
                    }
                    // Remove peers that are no longer present.
                    val currentIds = parsed.map { it.peerId }.toSet()
                    updated.keys.retainAll(currentIds)

                    localSelections = updated
                    errorMessage = null
                } catch (e: Exception) {
                    errorMessage = e.message
                }
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
        hasChanges = offerings.any { peer ->
            val s = localSelections[peer.peerId] ?: return@any false
            s.routes != peer.acceptedRoutes ||
                s.dns != peer.acceptedDns ||
                s.dnsSearch != peer.acceptedDnsSearch
        }
    }

    // Apply all local selections to the Go tunnel, persist config, and reconnect.
    fun applySelections() {
        applying = true
        scope.launch {
            try {
                for (peer in offerings) {
                    val sel = localSelections[peer.peerId] ?: continue
                    val selectionsJson = JSONObject().apply {
                        put("routes", JSONArray(sel.routes.toList()))
                        put("dns", JSONArray(sel.dns.toList()))
                        put("dns_search", JSONArray(sel.dnsSearch.toList()))
                    }.toString()

                    withContext(Dispatchers.IO) {
                        tunnel?.configurePeer(peer.peerId, selectionsJson)
                    }
                }

                // Persist the live tunnel's config (which now includes the
                // updated peer selections) to DataStore.
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

                    val sel = localSelections[peer.peerId]
                    PeerSection(
                        peer = peer,
                        selections = sel,
                        onToggle = { category, item, checked ->
                            toggle(peer.peerId, category, item, checked)
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
    selections: PeerSelections?,
    onToggle: (category: String, item: String, checked: Boolean) -> Unit
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
                checked = selections?.routes?.contains(route) == true,
                onCheckedChange = { checked -> onToggle("routes", route, checked) }
            )
        }
    }

    // DNS Servers
    if (peer.advertisedDns.isNotEmpty()) {
        SectionHeader("DNS Servers")
        for (dns in peer.advertisedDns) {
            CheckboxRow(
                label = dns,
                checked = selections?.dns?.contains(dns) == true,
                onCheckedChange = { checked -> onToggle("dns", dns, checked) }
            )
        }
    }

    // Search Domains
    if (peer.advertisedDnsSearch.isNotEmpty()) {
        SectionHeader("Search Domains")
        for (domain in peer.advertisedDnsSearch) {
            CheckboxRow(
                label = domain,
                checked = selections?.dnsSearch?.contains(domain) == true,
                onCheckedChange = { checked -> onToggle("dns_search", domain, checked) }
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
