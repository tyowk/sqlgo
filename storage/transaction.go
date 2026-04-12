package storage

import (
	"fmt"
	"sync"
)

type TxState int

const (
	TxIdle       TxState = 0
	TxActive     TxState = 1
	TxCommited   TxState = 2
	TxRolledBack TxState = 3
)

type savepoint struct {
	name  string
	pages map[uint32][PageSize]byte
}

type Transaction struct {
	mu         sync.Mutex
	pager      *Pager
	state      TxState
	saveState  map[uint32][PageSize]byte
	savepoints []savepoint
}

func NewTransaction(pager *Pager) *Transaction {
	return &Transaction{
		pager:     pager,
		state:     TxActive,
		saveState: make(map[uint32][PageSize]byte),
	}
}

func (tx *Transaction) SavePage(pg *Page) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if _, already := tx.saveState[pg.ID]; !already {
		tx.saveState[pg.ID] = pg.Data
	}
	for i := range tx.savepoints {
		sp := &tx.savepoints[i]
		if _, already := sp.pages[pg.ID]; !already {
			sp.pages[pg.ID] = pg.Data
		}
	}
}

func (tx *Transaction) Savepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != TxActive {
		return fmt.Errorf("transaction is not active")
	}
	tx.savepoints = append(tx.savepoints, savepoint{
		name:  name,
		pages: make(map[uint32][PageSize]byte),
	})
	return nil
}

func (tx *Transaction) ReleaseSavepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	for i := len(tx.savepoints) - 1; i >= 0; i-- {
		if tx.savepoints[i].name == name {
			tx.savepoints = tx.savepoints[:i]
			return nil
		}
	}
	return fmt.Errorf("savepoint '%s' does not exist", name)
}

func (tx *Transaction) RollbackToSavepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	idx := -1
	for i := len(tx.savepoints) - 1; i >= 0; i-- {
		if tx.savepoints[i].name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("savepoint '%s' does not exist", name)
	}
	sp := &tx.savepoints[idx]
	for pageID, data := range sp.pages {
		pg, err := tx.pager.GetPage(pageID)
		if err != nil {
			continue
		}
		pg.Data = data
		pg.Dirty = false
	}
	tx.savepoints = tx.savepoints[:idx+1]
	tx.savepoints[idx].pages = make(map[uint32][PageSize]byte)
	return nil
}

func (tx *Transaction) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != TxActive {
		return fmt.Errorf("transaction is not active")
	}
	tx.state = TxCommited
	tx.saveState = nil
	tx.savepoints = nil
	return tx.pager.FlushAll()
}

func (tx *Transaction) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != TxActive {
		return fmt.Errorf("transaction is not active")
	}
	for pageID, data := range tx.saveState {
		pg, err := tx.pager.GetPage(pageID)
		if err != nil {
			continue
		}
		pg.Data = data
		pg.Dirty = false
	}
	tx.state = TxRolledBack
	tx.saveState = nil
	tx.savepoints = nil
	return nil
}

func (tx *Transaction) IsActive() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.state == TxActive
}
