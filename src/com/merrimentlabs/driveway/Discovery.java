// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.os.Handler;
import android.os.Looper;

import java.net.Inet4Address;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executor;

/**
 * Finds file servers that announce themselves on the local network (Bonjour /
 * mDNS): Macs, Synology and other NAS boxes, Linux machines running Avahi.
 * Machines reached over Tailscale don't announce themselves; those are added
 * by name.
 */
final class Discovery {

  static final class Found {
    final String name;
    final String backend;
    final String host;
    final int port;
    final boolean https;

    Found(String name, String backend, String host, int port, boolean https) {
      this.name = name;
      this.backend = backend;
      this.host = host;
      this.port = port;
      this.https = https;
    }

    String key() {
      return backend + "|" + name;
    }
  }

  interface Listener {
    void onChanged(List<Found> found);
  }

  private static final String[][] TYPES = {
      {"_smb._tcp", "smb"},
      {"_sftp-ssh._tcp", "sftp"},
      {"_ssh._tcp", "sftp"},
      {"_nfs._tcp", "nfs"},
      {"_webdav._tcp", "webdav"},
      {"_webdavs._tcp", "webdav"},
  };

  private final NsdManager nsd;
  private final Listener listener;
  private final Handler main = new Handler(Looper.getMainLooper());
  private final Executor onMain = main::post;
  private final Map<String, Found> found = new LinkedHashMap<>();
  private final List<NsdManager.DiscoveryListener> discoveries = new ArrayList<>();
  private final List<NsdManager.ServiceInfoCallback> callbacks = new ArrayList<>();

  Discovery(Context ctx, Listener listener) {
    this.nsd = ctx.getSystemService(NsdManager.class);
    this.listener = listener;
  }

  /**
   * Servers to report as found instead of looking (set only by the debug
   * build's test harness, for screenshots and checks with made-up servers).
   */
  static volatile List<Found> standIn;

  void start() {
    List<Found> fake = standIn;
    if (fake != null) {
      for (Found f : fake) found.put(f.key(), f);
      main.post(this::publish);
      return;
    }
    for (String[] t : TYPES) {
      String type = t[0];
      String backend = t[1];
      NsdManager.DiscoveryListener l = new NsdManager.DiscoveryListener() {
        @Override
        public void onServiceFound(NsdServiceInfo info) {
          resolve(info, backend, type.equals("_webdavs._tcp"));
        }

        @Override
        public void onServiceLost(NsdServiceInfo info) {
          if (found.remove(backend + "|" + info.getServiceName()) != null) publish();
        }

        @Override
        public void onDiscoveryStarted(String serviceType) {}

        @Override
        public void onDiscoveryStopped(String serviceType) {}

        @Override
        public void onStartDiscoveryFailed(String serviceType, int errorCode) {
          Logs.i("discovery of " + serviceType + " failed: " + errorCode);
        }

        @Override
        public void onStopDiscoveryFailed(String serviceType, int errorCode) {}
      };
      try {
        nsd.discoverServices(type, NsdManager.PROTOCOL_DNS_SD, (android.net.Network) null, onMain, l);
        discoveries.add(l);
      } catch (RuntimeException e) {
        Logs.i("can't look for " + type + ": " + e.getMessage());
      }
    }
  }

  private void resolve(NsdServiceInfo info, String backend, boolean https) {
    NsdManager.ServiceInfoCallback cb = new NsdManager.ServiceInfoCallback() {
      @Override
      public void onServiceUpdated(NsdServiceInfo s) {
        String host = address(s.getHostAddresses());
        if (host == null) return;
        Found f = new Found(s.getServiceName(), backend, host, s.getPort(), https);
        // One entry per machine and kind (SSH announced twice is one server),
        // with its latest address. A server announcing WebDAV and WebDAVS keeps
        // the TLS entry: an http:// prefill would send the password in clear.
        Found old = found.get(f.key());
        if (old != null && old.https && !f.https) return;
        found.put(f.key(), f);
        publish();
      }

      @Override
      public void onServiceInfoCallbackRegistrationFailed(int errorCode) {}

      @Override
      public void onServiceLost() {}

      @Override
      public void onServiceInfoCallbackUnregistered() {}
    };
    try {
      nsd.registerServiceInfoCallback(info, onMain, cb);
      callbacks.add(cb);
    } catch (RuntimeException e) {
      Logs.i("can't resolve " + info.getServiceName() + ": " + e.getMessage());
    }
  }

  /** An IPv4 address if there is one (easier to read, and what most NAS boxes expect). */
  private static String address(List<InetAddress> addrs) {
    if (addrs == null || addrs.isEmpty()) return null;
    for (InetAddress a : addrs) if (a instanceof Inet4Address) return a.getHostAddress();
    return addrs.get(0).getHostAddress();
  }

  private void publish() {
    listener.onChanged(new ArrayList<>(found.values()));
  }

  void stop() {
    for (NsdManager.DiscoveryListener l : discoveries) {
      try {
        nsd.stopServiceDiscovery(l);
      } catch (RuntimeException ignored) {
      }
    }
    for (NsdManager.ServiceInfoCallback cb : callbacks) {
      try {
        nsd.unregisterServiceInfoCallback(cb);
      } catch (RuntimeException ignored) {
      }
    }
    discoveries.clear();
    callbacks.clear();
    found.clear(); // a fresh look next time: nothing stale offered
  }
}
