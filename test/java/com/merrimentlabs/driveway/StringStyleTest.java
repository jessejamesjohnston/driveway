// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import java.io.File;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import javax.xml.parsers.DocumentBuilderFactory;

import org.w3c.dom.Element;
import org.w3c.dom.Node;
import org.w3c.dom.NodeList;

/**
 * The wording rules every string in res/values/strings.xml follows, checked
 * on the text a person sees (XML entities and Android escapes decoded),
 * across string, plurals and string-array entries:
 *
 * - A single sentence has no closing period. A block of two or more
 *   sentences keeps them.
 * - A trailing ellipsis only marks something in progress ("Looking for file
 *   servers…", "Checking GitHub…"), never a label that opens a dialog, a
 *   picker or a screen.
 * - Two parts are joined with a middle dot ( · ) or a comma, never an em
 *   dash, an en dash, a hyphen standing in for a dash (" - " or "--") or a
 *   semicolon.
 * - American spelling.
 * - Natural contractions ("can't", "it's"), unless the words sit inside
 *   quotation marks.
 * - Lists of three or more take a serial comma. That check only prints a
 *   warning: it cannot tell a list from a phrase that happens to contain
 *   "and".
 *
 * Entries marked translatable="false" are checked too: a person still reads
 * them. Sentence case and plain wording are left to review. An entry may
 * break a rule only through ALLOWED, and every entry there says why.
 */
public final class StringStyleTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  enum Rule { SINGLE_SENTENCE_PERIOD, ELLIPSIS, DASH_OR_SEMICOLON_JOIN, UK_SPELLING, UNCONTRACTED }

  /** Exceptions, keyed by rule and resource name. Each value is the reason. */
  private static final Map<String, String> ALLOWED = new LinkedHashMap<>();
  static {
    ALLOWED.put(Rule.SINGLE_SENTENCE_PERIOD + " tagline",
        "The tagline is quoted word for word, period included, wherever Driveway is described (the Merriment Labs "
            + "site among them), so the app shows it the same way.");
  }

  private static final Pattern UK_SPELLINGS = Pattern.compile(
      "\\b(cancell(ed|ing)|colour(s|ed|ful|ing)?|favourites?|behaviours?|"
          + "centre[sd]?|organis(e|ed|es|ing|ation)|recognis(e|ed|es|ing|able))\\b",
      Pattern.CASE_INSENSITIVE);
  private static final Pattern UNCONTRACTED = Pattern.compile(
      "\\b(do not|cannot|will not|you are|it is|does not|is not)\\b", Pattern.CASE_INSENSITIVE);
  /** Text inside straight or curly double quotes, which may be someone else's words. */
  private static final Pattern QUOTED = Pattern.compile("\"[^\"]*\"|\u201c[^\u201d]*\u201d");
  private static final Pattern SENTENCE_END = Pattern.compile("[.!?](?=\\s|$)");
  /** A hyphen used as a dash: one with a space on each side, or two in a row. */
  private static final Pattern HYPHEN_AS_DASH = Pattern.compile("\\s-\\s|--");
  private static final Pattern SEMICOLON_JOIN = Pattern.compile(";\\s");
  private static final Pattern SERIAL_COMMA_MISSING = Pattern.compile("\\w[^,.;:\u00b7]*, [^,.;:\u00b7]+ (and|or) \\w");
  /** The first word of a label in progress: letters only, at least six of them, ending in "ing" ("Saving"). */
  private static final Pattern PROGRESS_VERB = Pattern.compile("[A-Za-z]{3,}ing");
  private static final Pattern UNICODE_ESCAPE = Pattern.compile("\\\\u([0-9a-fA-F]{4})");

  /** One piece of text: a string, or one item of a plurals or string-array. */
  static final class Entry {
    final String name, text;

    Entry(String name, String text) {
      this.name = name;
      this.text = text;
    }
  }

  /** Undoes Android's string escapes and its optional surrounding double quotes. */
  static String decodeAndroid(String raw) {
    String s = raw.trim();
    if (s.length() >= 2 && s.charAt(0) == '"' && s.charAt(s.length() - 1) == '"') s = s.substring(1, s.length() - 1);
    Matcher m = UNICODE_ESCAPE.matcher(s);
    StringBuffer u = new StringBuffer();
    while (m.find()) m.appendReplacement(u, Matcher.quoteReplacement(String.valueOf((char) Integer.parseInt(m.group(1), 16))));
    m.appendTail(u);
    s = u.toString();
    StringBuilder out = new StringBuilder();
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      if (c == '\\' && i + 1 < s.length()) {
        char n = s.charAt(++i);
        out.append(n == 'n' ? '\n' : n == 't' ? '\t' : n);
      } else {
        out.append(c);
      }
    }
    return out.toString();
  }

  /** Every entry in a strings file, decoded. XML entities are decoded by the parser. */
  static List<Entry> entries(File file) throws Exception {
    Element root = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file).getDocumentElement();
    List<Entry> out = new ArrayList<>();
    NodeList children = root.getChildNodes();
    for (int i = 0; i < children.getLength(); i++) {
      Node n = children.item(i);
      if (!(n instanceof Element)) continue;
      Element el = (Element) n;
      String name = el.getAttribute("name");
      switch (el.getTagName()) {
        case "string":
          out.add(new Entry(name, decodeAndroid(el.getTextContent())));
          break;
        case "plurals":
        case "string-array":
          NodeList items = el.getElementsByTagName("item");
          for (int j = 0; j < items.getLength(); j++) {
            Element item = (Element) items.item(j);
            String key = item.getAttribute("quantity").isEmpty() ? String.valueOf(j) : item.getAttribute("quantity");
            out.add(new Entry(name + "[" + key + "]", decodeAndroid(item.getTextContent())));
          }
          break;
        default:
      }
    }
    return out;
  }

  /** The rules text breaks, with a short note for each. */
  static Map<Rule, String> violations(String text) {
    Map<Rule, String> found = new LinkedHashMap<>();
    String t = text.trim();
    int sentences = 0;
    for (Matcher m = SENTENCE_END.matcher(t); m.find(); ) sentences++;
    if (t.endsWith(".") && !t.endsWith("...") && sentences == 1) {
      found.put(Rule.SINGLE_SENTENCE_PERIOD, "a single sentence ends in a period");
    }
    String beforeEllipsis = t.endsWith("\u2026") ? t.substring(0, t.length() - 1)
        : t.endsWith("...") ? t.substring(0, t.length() - 3) : null;
    // A state in progress starts with its "-ing" verb: "Looking for file servers…", "Checking GitHub…".
    if (beforeEllipsis != null && !PROGRESS_VERB.matcher(beforeEllipsis.trim().split(" ", 2)[0]).matches()) {
      found.put(Rule.ELLIPSIS, "a trailing ellipsis on something that is not in progress");
    }
    if (t.indexOf('\u2014') >= 0 || t.indexOf('\u2013') >= 0 || HYPHEN_AS_DASH.matcher(t).find()
        || SEMICOLON_JOIN.matcher(t).find()) {
      found.put(Rule.DASH_OR_SEMICOLON_JOIN, "a dash or semicolon join (use \u00b7 or a comma)");
    }
    Matcher uk = UK_SPELLINGS.matcher(t);
    if (uk.find()) found.put(Rule.UK_SPELLING, "UK spelling \"" + uk.group() + "\"");
    Matcher longForm = UNCONTRACTED.matcher(QUOTED.matcher(t.replace('\u2019', '\'')).replaceAll(""));
    if (longForm.find()) found.put(Rule.UNCONTRACTED, "\"" + longForm.group() + "\" reads better contracted");
    return found;
  }

  static boolean serialCommaMaybeMissing(String text) {
    return SERIAL_COMMA_MISSING.matcher(text).find();
  }

  private static void everyStringFollowsTheWordingRules(List<Entry> all) {
    check(all.size() > 250, "strings.xml parsed to " + all.size() + " entries");
    check(all.stream().anyMatch(e -> e.name.equals("remove_unsent[one]")), "plurals items are read");
    check(all.stream().anyMatch(e -> e.name.equals("source_sub")), "translatable=\"false\" strings are read");
    check(all.stream().anyMatch(e -> e.name.equals("optional") && e.text.equals(" (optional)")),
        "Android escapes and surrounding quotes are decoded");
    List<String> problems = new ArrayList<>();
    Set<String> used = new HashSet<>();
    for (Entry e : all) {
      for (Map.Entry<Rule, String> v : violations(e.text).entrySet()) {
        String key = v.getKey() + " " + e.name;
        if (ALLOWED.containsKey(key)) used.add(key);
        else problems.add(e.name + ": " + v.getValue() + ": \"" + e.text + "\"");
      }
      if (serialCommaMaybeMissing(e.text)) {
        System.out.println("WARNING " + e.name + ": a list of three or more may be missing its serial comma: \"" + e.text + "\"");
      }
    }
    for (String p : problems) System.out.println("  " + p);
    check(problems.isEmpty(), "every string follows the wording rules (" + problems.size() + " don't)");
    Set<String> stale = new HashSet<>(ALLOWED.keySet());
    stale.removeAll(used);
    check(stale.isEmpty(), "no allowlist entry outlives the string that needed it " + stale);
  }

  private static void everyAllowlistEntryNamesAStringAndSaysWhy(List<Entry> all) {
    Set<String> names = new HashSet<>();
    for (Entry e : all) names.add(e.name);
    for (Map.Entry<String, String> a : ALLOWED.entrySet()) {
      String name = a.getKey().split(" ", 2)[1];
      check(names.contains(name), a.getKey() + " names a string in strings.xml");
      check(!a.getValue().trim().isEmpty(), a.getKey() + " gives a reason");
    }
  }

  private static Set<Rule> rules(String text) {
    return violations(text).keySet();
  }

  private static void theRulesCatchWhatTheyShould() {
    check(rules("No servers yet.").equals(Set.of(Rule.SINGLE_SENTENCE_PERIOD)), "catches a single sentence's period");
    check(rules("Driveway is no longer signed in to %1$s.").equals(Set.of(Rule.SINGLE_SENTENCE_PERIOD)),
        "catches a period after a placeholder");
    check(rules("Change sign-in\u2026").equals(Set.of(Rule.ELLIPSIS)), "catches an ellipsis on a label");
    check(rules("Edit name...").equals(Set.of(Rule.ELLIPSIS)), "catches three dots on a label");
    check(rules("Save to device, then sharing\u2026").equals(Set.of(Rule.ELLIPSIS)),
        "an ellipsis needs the label's first word to be the -ing verb");
    for (String shortWord : new String[] {"Ring\u2026", "Bring\u2026", "Bring it\u2026", "Sing-ing\u2026"}) {
      check(rules(shortWord).equals(Set.of(Rule.ELLIPSIS)), "an ellipsis needs a letters-only -ing word of six or more: " + shortWord);
    }
    for (String join : new String[] {"Working offline \u2014 using saved files", "Paused \u2013 tap to resume",
        "Couldn't connect; try again", "Paused - tap to resume", "Paused -- tap to resume", "Paused--tap to resume",
        "%1$s - %2$s"}) {
      check(rules(join).equals(Set.of(Rule.DASH_OR_SEMICOLON_JOIN)), "catches a dash or semicolon join: " + join);
    }
    for (String uk : new String[] {"Sign-in cancelled", "Pick a colour", "Favourites", "Odd behaviour", "Centre it",
        "Organise", "Recognised"}) {
      check(rules(uk).equals(Set.of(Rule.UK_SPELLING)), "catches a UK spelling: " + uk);
    }
    for (String lng : new String[] {"Do not stop", "You cannot reach it", "It will not connect", "You are set",
        "It is slower", "It does not read", "This is not set"}) {
      check(rules(lng).equals(Set.of(Rule.UNCONTRACTED)), "catches an uncontracted form: " + lng);
    }
    check(serialCommaMaybeMissing("Letters, numbers, dots and hyphens"), "warns of a missing serial comma");
  }

  private static void theRulesLeaveGoodTextAlone() {
    for (String good : new String[] {
        "Looking for file servers\u2026",
        "Signing in as %1$s\u2026",
        "Checking GitHub\u2026",
        "Saving\u2026",
        "Connecting\u2026",
        "No servers yet",
        "%1$d%% \u00b7 %2$s of %3$s",
        "None found. You can still add a server below, including one you reach over Tailscale.",
        "Copyright \u00a9 2026 Jesse Johnston. Published by Merriment Labs.",
        "Letters, numbers, dots, and hyphens",
        "Sign-in canceled",
        "Don't stop, it's set, you're done",
        "The sign says \u201cDo not enter\u201d",
        "Couldn't sign in to %1$s. Try again. (%1$s said: %2$s)",
        "An IPv6 address with a port goes in brackets, like [2001:db8::1]:2222",
        "Add this line to ~/.ssh/authorized_keys on the server:",
        "A share, or share/folder. Leave empty to see every share.",
        "Port 1 to 65535",
        "github.com/jessejamesjohnston/driveway",
    }) {
      check(violations(good).isEmpty(), "leaves good text alone: " + good + " " + violations(good).keySet());
    }
  }

  private static void decodingUndoesAndroidEscapes() {
    check(decodeAndroid("Driveway\\'s key").equals("Driveway's key"), "decodes an escaped apostrophe");
    check(decodeAndroid("a\\nb").equals("a\nb"), "decodes a line break");
    check(decodeAndroid("\\u201c%1$s\\u201d").equals("\u201c%1$s\u201d"), "decodes a unicode escape");
    check(decodeAndroid("\"\\u0020(optional)\"").equals(" (optional)"), "keeps the space inside surrounding quotes");
    check(!decodeAndroid("\\\"quoted\\\"").contains("\\"), "decodes escaped quotes");
  }

  public static void main(String[] args) throws Exception {
    File strings = new File(System.getProperty("driveway.strings", "res/values/strings.xml"));
    List<Entry> all = entries(strings);
    everyStringFollowsTheWordingRules(all);
    everyAllowlistEntryNamesAStringAndSaysWhy(all);
    theRulesCatchWhatTheyShould();
    theRulesLeaveGoodTextAlone();
    decodingUndoesAndroidEscapes();
    System.out.println(failures == 0 ? "StringStyleTest: all passed" : "StringStyleTest: " + failures + " failed");
    if (failures > 0) System.exit(1);
  }
}
