package doubao

// 生成链路跨站点取号回归：豆包账号停用/失效时，Pick 自动落到 Dola 账号，
// 且返回的凭据带站点标识（生成编排据此切换请求域名）。

import "testing"

func TestPickFallsBackToDolaWhenDoubaoDisabled(t *testing.T) {
	s := newPoolTestService(t)
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao, Label: "豆包已停用"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "bbbbbbbbbbbbbbbbbbbb", Site: SiteDola, Label: "Dola可用"}); err != nil {
		t.Fatal(err)
	}
	// 停用豆包站点全部账号（与用户把豆包账号停用的场景一致）。
	if _, err := s.Status(SiteDoubao); err != nil {
		t.Fatal(err)
	}
	doubaoStatus, _ := s.Status(SiteDoubao)
	ids := make([]string, 0, len(doubaoStatus.Accounts))
	for _, a := range doubaoStatus.Accounts {
		ids = append(ids, a.ID)
	}
	if _, err := s.BatchOp("disable", SiteDoubao, ids, nil); err != nil {
		t.Fatal(err)
	}

	cred, err := s.Pick("")
	if err != nil {
		t.Fatalf("豆包停用后 Pick 应落到 Dola： %v", err)
	}
	if cred.Site != SiteDola || cred.Label != "Dola可用" {
		t.Fatalf("Pick 应返回 Dola 账号，got site=%q label=%q", cred.Site, cred.Label)
	}
}

func TestPickPrefersDoubaoWhenAvailable(t *testing.T) {
	s := newPoolTestService(t)
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao, Label: "豆包可用"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "bbbbbbbbbbbbbbbbbbbb", Site: SiteDola, Label: "Dola可用"}); err != nil {
		t.Fatal(err)
	}
	cred, err := s.Pick("")
	if err != nil {
		t.Fatal(err)
	}
	if cred.Site != SiteDoubao {
		t.Fatalf("豆包可用时应优先取豆包，got site=%q", cred.Site)
	}
}

func TestPickNeverReturnsJimeng(t *testing.T) {
	s := newPoolTestService(t)
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteJimeng, Label: "即梦"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pick(""); err == nil {
		t.Fatal("仅即梦账号时 Pick 应报错（即梦不走豆包生成协议）")
	}
}

func TestSiteOrigin(t *testing.T) {
	if got := siteOrigin(SiteDoubao); got != "https://www.doubao.com" {
		t.Fatalf("siteOrigin(doubao) = %q", got)
	}
	if got := siteOrigin(SiteDola); got != "https://www.dola.com" {
		t.Fatalf("siteOrigin(dola) = %q", got)
	}
	if got := siteOrigin(""); got != "https://www.doubao.com" {
		t.Fatalf("siteOrigin(空) = %q", got)
	}
}
