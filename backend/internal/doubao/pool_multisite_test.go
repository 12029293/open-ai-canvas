package doubao

// 账号池多站点语义回归：同表承载 doubao / dola / jimeng 三站账号，
// 状态列表、取号、批量操作、活跃指针都必须按站点隔离。

import (
	"fmt"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newPoolTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:doubao-pool-multisite-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.DoubaoAccount{}, &model.DoubaoPoolMeta{}); err != nil {
		t.Fatal(err)
	}
	return NewService(db)
}

func TestNormalizeSite(t *testing.T) {
	cases := map[string]string{
		"":         SiteDoubao,
		"doubao":   SiteDoubao,
		"DOUBAO":   SiteDoubao,
		"dola":     SiteDola,
		" Dola ":   SiteDola,
		"jimeng":   SiteJimeng,
		"unknown":  SiteDoubao,
		"doubao-x": SiteDoubao,
	}
	for input, want := range cases {
		if got := NormalizeSite(input); got != want {
			t.Fatalf("NormalizeSite(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPoolMultiSiteIsolation(t *testing.T) {
	s := newPoolTestService(t)

	// 各站点写入同值 sessionid（组合唯一索引下互不冲突）+ 站内多账号。
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao, Label: "豆包A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao, Label: "豆包A重复导入应更新"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDola}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteJimeng}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "bbbbbbbbbbbbbbbbbbbb", Site: SiteDola}); err != nil {
		t.Fatal(err)
	}

	doubaoStatus, err := s.Status(SiteDoubao)
	if err != nil {
		t.Fatal(err)
	}
	dolaStatus, err := s.Status(SiteDola)
	if err != nil {
		t.Fatal(err)
	}
	jimengStatus, err := s.Status(SiteJimeng)
	if err != nil {
		t.Fatal(err)
	}

	if doubaoStatus.Site != SiteDoubao || dolaStatus.Site != SiteDola || jimengStatus.Site != SiteJimeng {
		t.Fatal("Status 应回传归一化站点标识")
	}
	if doubaoStatus.AccountCount != 1 {
		t.Fatalf("豆包站点应只有 1 个账号（同 sessionid 重复导入按更新），got %d", doubaoStatus.AccountCount)
	}
	if doubaoStatus.Accounts[0].Label != "豆包A重复导入应更新" {
		t.Fatalf("重复导入应更新既有账号 label，got %q", doubaoStatus.Accounts[0].Label)
	}
	if dolaStatus.AccountCount != 2 {
		t.Fatalf("Dola 站点应有 2 个账号，got %d", dolaStatus.AccountCount)
	}
	if jimengStatus.AccountCount != 1 {
		t.Fatalf("即梦站点应有 1 个账号，got %d", jimengStatus.AccountCount)
	}
	if jimengStatus.Accounts[0].Label != "即梦账号 1" {
		t.Fatalf("默认命名应带站点名，got %q", jimengStatus.Accounts[0].Label)
	}
	for _, st := range []*PoolStatus{doubaoStatus, dolaStatus, jimengStatus} {
		for _, a := range st.Accounts {
			if a.Site != st.Site {
				t.Fatalf("账号 %s site=%s 混入站点 %s", a.ID, a.Site, st.Site)
			}
		}
	}

	// 取号按站点隔离：dola 取到的账号属于 dola，且成为 dola 站活跃账号。
	cred, err := s.PickSite(SiteDola, "")
	if err != nil {
		t.Fatal(err)
	}
	if cred.ID != dolaStatus.Accounts[0].ID {
		t.Fatalf("PickSite(dola) 应返回 dola 账号")
	}
	dolaAfter, _ := s.Status(SiteDola)
	if !dolaAfter.Accounts[0].Active {
		t.Fatal("取号后应设为站点活跃账号")
	}

	// 批量停用只影响目标站点。
	dolaIDs := []string{dolaAfter.Accounts[0].ID, dolaAfter.Accounts[1].ID}
	if _, err := s.BatchOp("disable", SiteDola, dolaIDs, nil); err != nil {
		t.Fatal(err)
	}
	dolaDisabled, _ := s.Status(SiteDola)
	if dolaDisabled.DisabledCount != 2 {
		t.Fatalf("Dola 批量停用后应为 2 个停用，got %d", dolaDisabled.DisabledCount)
	}
	jimengAfter, _ := s.Status(SiteJimeng)
	if jimengAfter.AvailableCount != 1 {
		t.Fatalf("停用 Dola 账号不应影响即梦站点，即梦可用=%d", jimengAfter.AvailableCount)
	}

	// MarkFailed 的站内自动换号不应跨站。
	doubaoPick, err := s.PickSite(SiteDoubao, "")
	if err != nil {
		t.Fatal(err)
	}
	switched, _, err := s.MarkFailed(doubaoPick.ID, MarkFailedOptions{Kind: FailKindRateLimited, Message: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if switched {
		t.Fatal("豆包站点只有 1 个账号时 MarkFailed 不应声称切换成功")
	}
}

func TestBulkImportMultiSite(t *testing.T) {
	s := newPoolTestService(t)
	result, err := s.BulkImport(BulkImportInput{
		Site: SiteJimeng,
		Text: "# 注释\n" + "cccccccccccccccccccc\n" + "sessionid=dddddddddddddddddddd; ttwid=1abc\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 2 || result.Failed != nil && len(result.Failed) != 0 {
		t.Fatalf("即梦批量导入应成功 2 行，got added=%d failed=%v", result.Added, result.Failed)
	}
	// 同样的凭据在豆包站点是独立账号（互不串站）。
	result, err = s.BulkImport(BulkImportInput{Site: SiteDoubao, Text: "cccccccccccccccccccc\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 {
		t.Fatalf("豆包站点导入同凭据应为新增，got added=%d", result.Added)
	}
}
