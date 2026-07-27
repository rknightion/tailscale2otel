#!/usr/bin/env python3
"""Tests for the changelog review lane (#446)."""

import unittest

import review_changelog as rc


FEED = """<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
  <item>
    <title>New network flow log destination</title>
    <link>https://example.invalid/a</link>
    <guid>changelog:2026-01-01-a</guid>
    <pubDate>Thu, 01 Jan 2026 00:00:00 GMT</pubDate>
    <description>&lt;ul&gt;&lt;li&gt;Flow log streaming to a new sink&lt;/li&gt;&lt;/ul&gt;</description>
  </item>
  <item>
    <title>Admin console button colour</title>
    <link>https://example.invalid/b</link>
    <guid>changelog:2026-01-02-b</guid>
    <pubDate>Fri, 02 Jan 2026 00:00:00 GMT</pubDate>
    <description>The button is now blue.</description>
  </item>
</channel></rss>
"""


def write(tmp, name, body):
    path = tmp / name
    path.write_text(body, encoding="utf-8")
    return str(path)


class FeedTest(unittest.TestCase):
    def setUp(self):
        import tempfile
        import pathlib
        self._dir = tempfile.TemporaryDirectory()
        self.tmp = pathlib.Path(self._dir.name)

    def tearDown(self):
        self._dir.cleanup()

    def test_it_parses_items(self):
        items = rc.load_feed(write(self.tmp, "f.xml", FEED))
        self.assertEqual(len(items), 2)
        self.assertEqual(items[0]["guid"], "changelog:2026-01-01-a")

    def test_an_empty_feed_is_an_error_not_a_clean_run(self):
        """'No entries' and 'nothing new' must not be the same outcome.

        A feed whose format changed would otherwise parse to zero items and the
        lane would report success forever while watching nothing.
        """
        empty = '<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>'
        with self.assertRaises(SystemExit) as ctx:
            rc.load_feed(write(self.tmp, "e.xml", empty))
        self.assertEqual(ctx.exception.code, 2)

    def test_a_feed_with_a_dtd_is_refused(self):
        """Entity-expansion defence: both XXE and billion-laughs need a DTD.

        defusedxml is unavailable under the stdlib-only rule, so the parser is
        never handed a document carrying one.
        """
        bomb = ('<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol">]>'
                '<rss version="2.0"><channel><item><title>x</title>'
                '<guid>g</guid></item></channel></rss>')
        with self.assertRaises(SystemExit) as ctx:
            rc.load_feed(write(self.tmp, "bomb.xml", bomb))
        self.assertEqual(ctx.exception.code, 2)

    def test_a_non_https_url_is_refused(self):
        with self.assertRaises(SystemExit) as ctx:
            rc.load_feed("http://example.invalid/feed.xml")
        self.assertEqual(ctx.exception.code, 2)


class RelevanceTest(unittest.TestCase):
    def test_an_observability_surface_matches(self):
        item = {"title": "New network flow log destination",
                "text": "Flow log streaming to a new sink"}
        self.assertIn("flow log", rc.is_relevant(item))

    def test_an_unrelated_entry_does_not(self):
        item = {"title": "Admin console button colour", "text": "The button is now blue."}
        self.assertEqual(rc.is_relevant(item), [])

    def test_bare_api_is_not_a_term(self):
        """Deliberate: the daily operation-set lane already forces a decision on
        every published API operation, so matching "api" here would duplicate a
        stronger signal — and it was the single largest source of noise."""
        item = {"title": "API rate limits adjusted", "text": "The API now allows more."}
        self.assertEqual(rc.is_relevant(item), [])


class BaselineTest(unittest.TestCase):
    def test_reviewed_entries_are_not_re_proposed(self):
        items = [{"guid": "g1", "title": "flow log thing", "text": "flow log"}]
        self.assertEqual(len(rc.review(items, {})), 1)
        reviewed = {"g1": {"verdict": "rejected", "note": "no operator use case"}}
        self.assertEqual(rc.review(items, reviewed), [],
                         "a recorded NEGATIVE verdict must suppress the entry; otherwise "
                         "every run re-raises a decision already made")

    def test_the_real_baseline_is_fully_and_validly_dispositioned(self):
        reviewed = rc.load_baseline()
        self.assertGreater(len(reviewed), 100)
        for guid, entry in reviewed.items():
            self.assertIn(entry.get("verdict"), rc.VERDICTS, "bad verdict on %s" % guid)

    def test_an_unknown_verdict_is_rejected(self):
        # load_baseline() reads a fixed path, so exercise the validation rule it
        # applies rather than the file read.
        self.assertNotIn("looks-fine", rc.VERDICTS)
        self.assertIn("rejected", rc.VERDICTS)
        self.assertIn("not-relevant", rc.VERDICTS)


if __name__ == "__main__":
    unittest.main()
