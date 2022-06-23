package repositories

import (
	"db_forum/app/models"
	"db_forum/pkg"
	"db_forum/pkg/queries"
	"fmt"
	"time"

	"github.com/jackc/pgx"
	_ "github.com/lib/pq"
)

type ThreadRepository interface {
	Create(thread *models.Thread) (err error)
	//GetThread(slugOrId interface{}) (*models.Thread, error)
	GetVotes(id int64) (votesAmount int32, err error)
	Update(thread *models.Thread) error
	CreatePosts(thread *models.Thread, posts *models.Posts) error
	GetPostsTree(id int64, limit, since int, desc bool) (posts *[]models.Post, err error)
	GetPostsParentTree(id int64, limit, since int, desc bool) (posts *[]models.Post, err error)
	GetPostsFlat(id int64, limit, since int, desc bool) (posts *[]models.Post, err error)
	GetBySlug(slug string) (thread *models.Thread, err error)
	GetById(id int64) (thread *models.Thread, err error)
}

type ThreadRepositoryImpl struct {
	db *pgx.ConnPool
}

func MakeThreadRepository(db *pgx.ConnPool) ThreadRepository {
	return &ThreadRepositoryImpl{db: db}
}

func (threadRepository *ThreadRepositoryImpl) GetBySlug(slug string) (thread *models.Thread, err error) {
	thread = &models.Thread{}
	err = threadRepository.db.QueryRow(queries.ThreadGetSlug, slug).
		Scan(&thread.ID, &thread.Title, &thread.Author, &thread.Forum, &thread.Message, &thread.Votes, &thread.Slug, &thread.Created)
	return
}

func (threadRepository *ThreadRepositoryImpl) GetById(id int64) (thread *models.Thread, err error) {
	thread = &models.Thread{}
	err = threadRepository.db.QueryRow(queries.ThreadGetId, id).
		Scan(&thread.ID, &thread.Title, &thread.Author, &thread.Forum, &thread.Message, &thread.Votes, &thread.Slug, &thread.Created)
	return
}

func (threadRepository *ThreadRepositoryImpl) Create(thread *models.Thread) (err error) {
	err = threadRepository.db.QueryRow(queries.ThreadCreate, thread.Title, thread.Author, thread.Forum, thread.Message, thread.Slug, thread.Created).
		Scan(
			&thread.ID,
			&thread.Created)
	return
}

//func (threadRepository *ThreadRepositoryImpl) GetThread(slugOrId interface{}) (*models.Thread, error) {
//	thread := &models.Thread{}
//	var err error
//	switch slugOrId.(type) {
//	case string:
//		err = threadRepository.db.QueryRow(queries.ThreadGetSlug, slugOrId).
//			Scan(&thread.ID, &thread.Title, &thread.Author, &thread.Forum, &thread.Message, &thread.Votes, &thread.Slug, &thread.Created)
//	case int64:
//		id, _ := strconv.Atoi(slugOrId.(string))
//		err = threadRepository.db.QueryRow(queries.ThreadGetId, int64(id)).
//			Scan(&thread.ID, &thread.Title, &thread.Author, &thread.Forum, &thread.Message, &thread.Votes, &thread.Slug, &thread.Created)
//	}
//	return thread, err
//}

func (threadRepository *ThreadRepositoryImpl) GetVotes(id int64) (int32, error) {
	var votes int32
	err := threadRepository.db.QueryRow(queries.ThreadVotes, id).Scan(&votes)
	return votes, err
}

func (threadRepository *ThreadRepositoryImpl) Update(thread *models.Thread) error {
	_, err := threadRepository.db.Exec(queries.ThreadUpdate, thread.Title, thread.Message, thread.ID)
	return err
}

func (threadRepository *ThreadRepositoryImpl) createPartPosts(thread *models.Thread, posts *models.Posts, from, to int, created time.Time, createdFormatted string) (err error) {
	query := "insert into posts (parent, author, message, forum, thread, created) values "
	args := make([]interface{}, 0, 0)

	j := 0
	for i := from; i < to; i++ {
		(*posts)[i].Forum = thread.Forum
		(*posts)[i].Thread = thread.ID
		(*posts)[i].Created = createdFormatted
		query += fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d),", j*6+1, j*6+2, j*6+3, j*6+4, j*6+5, j*6+6)
		if (*posts)[i].Parent != 0 {
			args = append(args, (*posts)[i].Parent, (*posts)[i].Author, (*posts)[i].Message, thread.Forum, thread.ID, created)
		} else {
			args = append(args, nil, (*posts)[i].Author, (*posts)[i].Message, thread.Forum, thread.ID, created)
		}
		j++
	}
	query = query[:len(query)-1]
	query += " RETURNING id;"

	isSuccess := false
	k := 0

	for !isSuccess {

		resultRows, err := threadRepository.db.Query(query, args...)
		if err != nil {
			fmt.Println(err)
			return pkg.ErrParentPostNotExist
		}
		defer resultRows.Close()

		for i := from; resultRows.Next(); i++ {
			isSuccess = true
			var id int64
			if err = resultRows.Scan(&id); err != nil {
				return err
			}
			(*posts)[i].ID = id
		}
		k++
		if k >= 3 {
			break
		}
	}
	return
}

func (threadRepository *ThreadRepositoryImpl) CreatePosts(thread *models.Thread, posts *models.Posts) (err error) {
	created := time.Now()
	createdFormatted := created.Format(time.RFC3339)

	parts := len(*posts) / 20
	for i := 0; i < parts+1; i++ {
		if i == parts {
			if i*20 != len(*posts) {
				err = threadRepository.createPartPosts(thread, posts, i*20, len(*posts), created, createdFormatted)
				if err != nil {
					return err
				}
			}
		} else {
			err = threadRepository.createPartPosts(thread, posts, i*20, i*20+20, created, createdFormatted)
			if err != nil {
				return err
			}
		}
	}
	return
}

func (threadRepository *ThreadRepositoryImpl) GetPostsTree(id int64, limit, since int, desc bool) (posts *[]models.Post, err error) {
	var rows *pgx.Rows
	query := "select id, coalesce(parent, 0), author, message, is_edited, forum, thread, created from posts "
	if since == -1 {
		if desc {
			rows, err = threadRepository.db.Query(query+queries.ThreadTreeSinceDesc, id, limit)
		} else {
			rows, err = threadRepository.db.Query(query+queries.ThreadTreeSince, id, limit)
		}
	} else {
		if desc {
			rows, err = threadRepository.db.Query(query+queries.ThreadTreeDesc, id, since, limit)
		} else {
			rows, err = threadRepository.db.Query(query+queries.ThreadTree, id, since, limit)
		}
	}

	if err != nil {
		return
	}
	defer rows.Close()

	posts = new([]models.Post)
	for rows.Next() {
		post := models.Post{}
		postTime := time.Time{}

		err = rows.Scan(&post.ID, &post.Parent, &post.Author, &post.Message, &post.IsEdited, &post.Forum, &post.Thread, &postTime)
		if err != nil {
			return
		}

		post.Created = postTime.Format(time.RFC3339)
		*posts = append(*posts, post)
	}

	return
}

func (threadRepository *ThreadRepositoryImpl) GetPostsParentTree(threadID int64, limit, since int, desc bool) (posts *[]models.Post, err error) {
	var rows *pgx.Rows
	query := "select id, coalesce(parent, 0), author, message, is_edited, forum, thread, created from posts where path[1] IN "
	if since == -1 {
		if desc {
			rows, err = threadRepository.db.Query(query+queries.ThreadParentTreeSinceDesc, threadID, limit)
		} else {
			rows, err = threadRepository.db.Query(query+queries.ThreadParentTreeSince, threadID, limit)
		}
	} else {
		if desc {
			rows, err = threadRepository.db.Query(query+queries.ThreadParentTreeDesc, threadID, since, limit)
		} else {
			rows, err = threadRepository.db.Query(query+queries.ThreadParentTree, threadID, since, limit)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	posts = new([]models.Post)
	for rows.Next() {
		post := models.Post{}
		postTime := time.Time{}

		err = rows.Scan(&post.ID, &post.Parent, &post.Author, &post.Message, &post.IsEdited, &post.Forum, &post.Thread, &postTime)
		if err != nil {
			return
		}

		post.Created = postTime.Format(time.RFC3339)
		*posts = append(*posts, post)
	}

	return
}

func (threadRepository *ThreadRepositoryImpl) GetPostsFlat(id int64, limit, since int, desc bool) (posts *[]models.Post, err error) {
	var rows *pgx.Rows
	query := "select id, coalesce(parent, 0), author, message, is_edited, forum, thread, created from posts where thread = $1 "
	if since == -1 {
		if desc {
			rows, err = threadRepository.db.Query(query+queries.ThreadFlatSinceDesc, id, limit)
		} else {
			rows, err = threadRepository.db.Query(query+queries.ThreadFlatSince, id, limit)
		}
	} else {
		if desc {
			rows, err = threadRepository.db.Query(query+queries.ThreadFlatDesc, id, since, limit)
		} else {
			rows, err = threadRepository.db.Query(query+queries.ThreadFlat, id, since, limit)
		}
	}
	if err != nil {
		return
	}

	defer rows.Close()
	posts = new([]models.Post)
	for rows.Next() {
		post := models.Post{}
		postTime := time.Time{}

		err = rows.Scan(&post.ID, &post.Parent, &post.Author, &post.Message, &post.IsEdited, &post.Forum, &post.Thread, &postTime)
		if err != nil {
			return
		}

		post.Created = postTime.Format(time.RFC3339)
		*posts = append(*posts, post)
	}

	return
}
