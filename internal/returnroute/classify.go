package returnroute

// Rules adapted from yyy622hhh/komari-theme-transit at ee9f31c.
// Copyright notices and MIT terms are retained in third_party/transit-LICENSE.
// Prefix matches describe known backbone ranges; they are not live BGP lookups.
import "net/netip"

func hopASN(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return ""
	}
	b := a.As4()
	if b[0] == 223 {
		if b[1] == 118 && b[2] == 32 || b[1] == 120 && b[2] >= 128 {
			return "AS58807"
		}
		if b[1] == 119 {
			n := b[2]
			if n >= 8 && n <= 15 || n >= 26 && n <= 29 || n >= 32 && n <= 37 || n == 74 || n == 75 || n == 88 || n == 89 || n == 100 || n == 252 || n == 253 {
				return "AS58807"
			}
		}
		if b[1] >= 118 && b[1] <= 121 {
			return "AS58453"
		}
	}
	switch [2]byte{b[0], b[1]} {
	case [2]byte{59, 43}:
		return "AS4809"
	case [2]byte{202, 97}:
		return "AS4134"
	case [2]byte{218, 105}, [2]byte{210, 51}:
		return "AS9929"
	case [2]byte{202, 77}, [2]byte{43, 252}, [2]byte{61, 14}:
		return "AS10099"
	case [2]byte{219, 158}:
		return "AS4837"
	case [2]byte{221, 183}, [2]byte{111, 24}:
		return "AS9808"
	case [2]byte{69, 194}, [2]byte{203, 22}:
		return "AS23764"
	}
	return ""
}

func classify(carrier string, hops []Hop) Classification {
	position := func(asn string) (int, int) {
		first, count := -1, 0
		for i, hop := range hops {
			if hop.ASN == asn {
				if first < 0 {
					first = i
				}
				count++
			}
		}
		return first, count
	}
	result := func(code, label, grade, confidence, evidence string) Classification {
		return Classification{code, label, grade, confidence, evidence}
	}
	unknown := func(code, label, evidence string) Classification {
		return result(code, label, "", "inconclusive", evidence)
	}
	switch carrier {
	case "CT":
		cn2, n := position("AS4809")
		ordinary, m := position("AS4134")
		ctg, _ := position("AS23764")
		if cn2 >= 0 {
			if n < 2 {
				return result("ct_cn2_mixed", "电信 CN2 混合", "优质", "mixed", "仅见一跳 AS4809，不能确认 CN2 GIA")
			}
			if ordinary < 0 || cn2 < ordinary && m <= 1 {
				return result("ct_cn2_gia", "电信 CN2 GIA", "精品", "confirmed", "至少两跳 AS4809，之前未见 AS4134 骨干段")
			}
			if cn2 < ordinary {
				return result("ct_cn2_mixed", "电信 CN2 混合", "优质", "mixed", "CN2 之后出现多跳 AS4134 骨干段")
			}
			return result("ct_cn2_gt", "电信 CN2 GT", "优质", "mixed", "AS4134 在 AS4809 段之前出现")
		}
		if ctg >= 0 {
			return result("ct_ctgnet", "电信 CTGNET", "精品", "confirmed", "观察到 AS23764 骨干段")
		}
		if m > 1 {
			return result("ct_163", "电信 163", "普通", "confirmed", "多跳 AS4134，未观察到精品骨干段")
		}
		if m == 1 {
			return unknown("ct_destination_only", "仅见电信目的网", "单跳 AS4134 不足以确认骨干线路")
		}
		return unknown("ct_unknown", "未识别电信骨干", "未观察到 AS4809、AS23764 或 AS4134")
	case "CU":
		premium, _ := position("AS9929")
		cug, _ := position("AS10099")
		ordinary, n := position("AS4837")
		if premium >= 0 {
			if ordinary >= 0 && ordinary < premium {
				return result("cu_9929_mixed", "联通 9929 混合", "优质", "mixed", "AS4837 在 AS9929 段之前出现")
			}
			return result("cu_9929", "联通 9929", "精品", "confirmed", "观察到 AS9929，之前未见 AS4837 段")
		}
		if cug >= 0 {
			return result("cu_cug", "联通 CUG", "优质", "confirmed", "观察到 AS10099，未见 AS9929")
		}
		if n > 1 {
			return result("cu_4837", "联通 4837", "普通", "confirmed", "多跳 AS4837，未观察到精品骨干段")
		}
		if n == 1 {
			return unknown("cu_destination_only", "仅见联通目的网", "单跳 AS4837 不足以确认骨干线路")
		}
		return unknown("cu_unknown", "未识别联通骨干", "未观察到 AS9929、AS10099 或 AS4837")
	case "CM":
		premium, _ := position("AS58807")
		cmi, _ := position("AS58453")
		cmnet, _ := position("AS9808")
		if premium >= 0 {
			if cmi >= 0 && cmi < premium {
				return result("cm_cmin2_mixed", "移动 CMIN2 混合", "优质", "mixed", "AS58453 在 AS58807 段之前出现")
			}
			return result("cm_cmin2", "移动 CMIN2", "精品", "confirmed", "观察到 AS58807，之前未见 AS58453 段")
		}
		if cmi >= 0 {
			return result("cm_cmi", "移动 CMI", "普通", "confirmed", "观察到 AS58453，未见 CMIN2 段")
		}
		if cmnet >= 0 {
			return result("cm_cmnet", "移动 CMNET", "普通", "confirmed", "观察到 AS9808，未见国际精品骨干段")
		}
		return unknown("cm_unknown", "未识别移动骨干", "未观察到 AS58807、AS58453 或 AS9808")
	}
	return unknown("unknown", "线路证据不足", "未知运营商")
}
