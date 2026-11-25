// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Typeface;
import android.os.Bundle;
import android.view.KeyEvent;
import android.widget.LinearLayout;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;

/**
 * Open-source licenses: Driveway's own license first, then every component
 * it is built with (assets/licenses.json, made by tools/licenses.py), each
 * opening its full license text as a page of its own. MPL-2.0 components
 * say where their source is.
 */
public final class LicensesActivity extends Activity {
  /** Which text to show: a component's index, OWN for Driveway's license; absent for the list. */
  static final String EXTRA_ITEM = "item";
  static final int OWN = -1;

  private static JSONObject data;

  static synchronized JSONObject data(Activity a) throws Exception {
    if (data == null) {
      try (InputStream in = a.getAssets().open("licenses.json")) {
        data = new JSONObject(new String(in.readAllBytes(), StandardCharsets.UTF_8));
      }
    }
    return data;
  }

  @Override
  protected void onCreate(Bundle savedInstanceState) {
    super.onCreate(savedInstanceState);
    LinearLayout col = Ui.page(this);
    JSONObject d;
    try {
      d = data(this);
    } catch (Exception e) {
      col.addView(Ui.topBar(this, getString(R.string.licenses), v -> finish()));
      col.addView(Ui.secondary(this, getString(R.string.licenses_unreadable)));
      return;
    }
    if (getIntent().hasExtra(EXTRA_ITEM)) {
      showText(col, d, getIntent().getIntExtra(EXTRA_ITEM, OWN));
    } else {
      showList(col, d);
    }
  }

  private void showList(LinearLayout col, JSONObject d) {
    col.addView(Ui.topBar(this, getString(R.string.licenses), v -> finish()));
    LinearLayout own = Ui.card(this);
    own.addView(Ui.cardRow(this, null, getString(R.string.app_name), getString(R.string.license_own), Ui.chevron(this),
        v -> open(OWN)));
    col.addView(Ui.gap(this, 8));
    col.addView(own);
    TextView intro = Ui.secondary(this, getString(R.string.licenses_intro));
    intro.setPadding(Ui.dp(this, 4), Ui.dp(this, 16), Ui.dp(this, 4), Ui.dp(this, 8));
    col.addView(intro);
    LinearLayout list = Ui.card(this);
    JSONArray comps = d.optJSONArray("components");
    for (int i = 0; comps != null && i < comps.length(); i++) {
      JSONObject c = comps.optJSONObject(i);
      if (i > 0) list.addView(Ui.divider(this));
      String version = c.optString("version");
      String sub = version.isEmpty() ? c.optString("license") : version + " · " + c.optString("license");
      int at = i;
      list.addView(Ui.cardRow(this, null, c.optString("name"), sub, null, v -> open(at)));
    }
    col.addView(list);
  }

  private void open(int item) {
    startActivity(new Intent(this, LicensesActivity.class).putExtra(EXTRA_ITEM, item));
  }

  /** One license as a page: long texts are cramped in a dialog on a phone and awkward on a laptop. */
  private void showText(LinearLayout col, JSONObject d, int item) {
    String title;
    String body;
    if (item == OWN) {
      title = getString(R.string.app_name);
      body = d.optString("own");
    } else {
      JSONObject c = d.optJSONArray("components").optJSONObject(item);
      title = c.optString("name");
      String text = d.optJSONArray("texts").optString(c.optInt("text"));
      String source = c.optString("source");
      body = source.isEmpty() ? text : getString(R.string.license_source, source) + "\n\n" + text;
    }
    col.addView(Ui.topBar(this, title, v -> finish()));
    LinearLayout card = Ui.card(this);
    TextView t = Ui.text(this, body, 12.5f, 400);
    t.setTypeface(Typeface.MONOSPACE);
    t.setTextIsSelectable(true);
    int pad = Ui.dp(this, 16);
    t.setPadding(pad, pad, pad, pad);
    card.addView(t);
    col.addView(Ui.gap(this, 8));
    col.addView(card);
  }

  @Override
  public boolean onKeyUp(int keyCode, KeyEvent event) {
    // Escape closes the page, as Back does.
    if (keyCode == KeyEvent.KEYCODE_ESCAPE && event.hasNoModifiers()) {
      finish();
      return true;
    }
    return super.onKeyUp(keyCode, event);
  }
}
