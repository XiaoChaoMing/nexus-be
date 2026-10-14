package search_test

import (
	"context"
	"testing"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/search"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	repo.SearchRepo
	repo.ChannelRepo
	repo.CategoryRepo

	docs  []entity.SearchDoc
	gotQ  string
	limit int
}

func (f *fakeRepo) Suggest(_ context.Context, q string, _ []entity.DocType, limit int) ([]entity.SearchDoc, error) {
	f.gotQ, f.limit = q, limit

	return f.docs, nil
}

func (f *fakeRepo) Search(_ context.Context, q string, _ entity.DocType, _ *repo.SearchCursor, limit int) ([]entity.SearchDoc, error) {
	f.gotQ, f.limit = q, limit

	return f.docs, nil
}

func newUC(f *fakeRepo) *search.UseCase {
	l := logger.New("error")

	return search.New(f, f, enrich.New(f, nil, l), l)
}

func TestSuggest(t *testing.T) {
	t.Parallel()

	f := &fakeRepo{docs: []entity.SearchDoc{
		{Type: entity.DocVOD, ID: "v"}, {Type: entity.DocCategory, ID: "c"}, {Type: entity.DocChannel, ID: "ch"},
	}}
	uc := newUC(f)

	groups, err := uc.Suggest(context.Background(), "  Liên minh ", nil, 0)
	require.NoError(t, err)
	require.Equal(t, "Liên minh", f.gotQ)
	require.Equal(t, entity.SearchGroupMax, f.limit)
	require.Len(t, groups, 3)
	require.Equal(t, []entity.DocType{entity.DocChannel, entity.DocCategory, entity.DocVOD},
		[]entity.DocType{groups[0].Type, groups[1].Type, groups[2].Type}, "display order, empty groups left out")

	groups, err = uc.Suggest(context.Background(), "lien", []entity.DocType{entity.DocCategory}, 3)
	require.NoError(t, err)
	require.Len(t, groups, 1)

	for _, q := range []string{"a", " a ", ""} {
		_, err = uc.Suggest(context.Background(), q, nil, 5)
		require.ErrorIs(t, err, entity.ErrValidation)
		require.Equal(t, map[string]string{"q": entity.DetailLength}, entity.DetailsOf(err))
	}
}

func TestSearchPaging(t *testing.T) {
	t.Parallel()

	f := &fakeRepo{docs: []entity.SearchDoc{
		{Type: entity.DocCategory, ID: "0199c0de-0000-7000-8000-000000000001", Rank: 9},
		{Type: entity.DocCategory, ID: "0199c0de-0000-7000-8000-000000000002", Rank: 8},
	}}
	uc := newUC(f)

	page, err := uc.Search(context.Background(), "esports", entity.DocCategory, "", 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, 2, f.limit, "one more than asked to know whether a next page exists")
	require.NotEmpty(t, page.NextCursor)

	_, err = uc.Search(context.Background(), "esports", entity.DocCategory, page.NextCursor, 1)
	require.NoError(t, err)

	_, err = uc.Search(context.Background(), "esports", entity.DocCategory, "bm90LWpzb24", 1)
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"cursor": entity.DetailInvalid}, entity.DetailsOf(err))
}
