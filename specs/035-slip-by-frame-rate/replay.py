# The count of specs/035-slip-by-frame-rate replayed under the rule of 031
# and the candidates of D2 and D3 (research, "The rules replayed"): python3
# replay.py vnc-a.txt vnc-b.txt fast-ws1.txt, the batches of pkg/ui/testdata.

import sys

COUNTED = {"carousel", "grid", "hover", "locate"}


def read(path):
    period, runs = None, []
    for line in open(path):
        f = line.split()
        if f[:2] == ["#", "period_us"]:
            period = float(f[2]) / 1e6
        elif f and f[0] != "#":
            runs.append((int(f[0]), f[1], [int(x) / 1e6 for x in f[2:]]))
    return period, runs


class Old:
    name = "3 late of 60"

    def __init__(self):
        self.ring = []

    def frame(self, d, p):
        self.ring = (self.ring + [d > 1.25 * p])[-60:]
        return sum(self.ring) >= 3, None


class Mean:
    def __init__(self, w, share, windows):
        self.w, self.share, self.windows, self.iv = w, share, windows, []
        self.name = "%g s, %d %%%s" % (w, round(share * 100), ", two" if windows == 2 else "")

    def window(self, end):
        s = 0
        for i in range(end - 1, -1, -1):
            s += self.iv[i]
            if s >= self.w:
                return end - i, s
        return None

    def frame(self, d, p):
        self.iv = (self.iv + [d])[-4096:]
        end, shares = len(self.iv), []
        for _ in range(self.windows):
            win = self.window(end)
            if win is None:
                return False, shares[0] if shares else None
            n, s = win
            shares.append(n * p / s)
            end -= n
        return all(x < self.share for x in shares), shares[0]


RULES = [Old] + [lambda w=w, s=s, k=k: Mean(w, s, k)
                 for w in (0.5, 1.0) for s in (0.75, 0.66) for k in (1, 2)]


def replay(rule, period, runs, prefill, before=()):
    for _ in range(round(prefill / period)):
        rule.frame(period, period)
    for a, kind, intervals in before:
        for d in intervals:
            if kind in COUNTED and rule.frame(d, period)[0]:
                return "before"
    span, low, act = 0.0, None, None
    for a, kind, intervals in runs:
        if kind not in COUNTED:
            continue
        for d in intervals:
            span += d
            still, share = rule.frame(d, period)
            if share is not None:
                low = share if low is None else min(low, share)
            if still:
                return "%.2f s" % span
    return "never" + ("" if low is None else " (%.1f %%)" % (low * 100))


def row(label, period, runs, prefill=0.0, before=()):
    print("| " + label + " | " + " | ".join(replay(r(), period, runs, prefill, before) for r in RULES))


def steady(pattern, seconds):
    out = []
    while sum(out) < seconds:
        out += pattern
    return [(1, "carousel", out)]


if __name__ == "__main__":
    print("| Case | " + " | ".join(r().name for r in RULES))
    a, b, ws1 = (read(p) for p in sys.argv[1:4])
    row("A, 165 Hz", *a)
    row("B, 165 Hz", *b)
    row("ws1, 144 Hz", *ws1)
    row("ws1, the window full before", *ws1, prefill=1.0)
    k = (1 / 60) / b[0]
    row("B scaled to 60 Hz", 1 / 60, [(x, kd, [d * k for d in iv]) for x, kd, iv in b[1]])
    row("B, then a load of 30 ms", b[0], steady([0.030], 2.0), before=b[1])
    for hz, p in ((144, 0.006944525), (165, 1 / 165), (60, 1 / 60)):
        for load in (0.030, 0.050):
            row("%d Hz, a load of %d ms" % (hz, load * 1000), p, steady([load], 2.0))
            row("%d Hz, a load of %d ms, the window full" % (hz, load * 1000), p, steady([load], 2.0), 1.0)
        row("%d Hz, every other slot lost" % hz, p, steady([2 * p], 2.0), 1.0)
        row("%d Hz, every third slot lost" % hz, p, steady([p, 2 * p], 2.0), 1.0)
        for pause in (0.100, 0.150, 0.200, 0.300):
            row("%d Hz, a pause of %d ms" % (hz, pause * 1000), p,
                [(1, "carousel", [pause, 0.0004] + [p] * round(1.0 / p))], 1.0)
    row("144 Hz, the stalls of ws1 activation 10 over and over", 0.006944525,
        steady([0.006944525] * 4 + [0.0188, 0.00037, 0.00154, 0.0174, 0.00042, 0.00315, 0.00876], 3.0), 1.0)
