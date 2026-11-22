<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema as DbSchema;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Query\FieldType;
use PouryDev\LaravelKit\Query\Lister;
use PouryDev\LaravelKit\Query\Params;
use PouryDev\LaravelKit\Query\Schema;
use PouryDev\LaravelKit\Tests\TestCase;

final class Person extends Model
{
    protected $table = 'people';

    public $timestamps = false;

    protected $guarded = [];
}

final class QueryTest extends TestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        DbSchema::create('people', function (Blueprint $t) {
            $t->increments('id');
            $t->string('uid', 36);
            $t->string('name');
            $t->string('role');
            $t->integer('age');
            $t->boolean('active')->default(true);
            $t->string('notes')->nullable();
            $t->dateTime('created_at');
            $t->dateTime('deleted_at')->nullable();
        });
        foreach ([
            ['Ada Lovelace', 'admin', 36, true, '2026-01-10 10:00:00', null],
            ['Alan Turing', 'editor', 41, true, '2026-01-20 10:00:00', null],
            ['Grace Hopper', 'editor', 85, false, '2026-02-05 10:00:00', '2026-03-01 00:00:00'],
            ['100% Real_Name', 'viewer', 20, true, '2026-02-10 10:00:00', null],
            ['Linus', 'viewer', 55, true, '2026-02-11 10:00:00', null],
        ] as $i => [$n, $r, $a, $act, $c, $d]) {
            Person::create(['uid' => sprintf('00000000-0000-4000-8000-%012d', $i + 1), 'name' => $n, 'role' => $r, 'age' => $a, 'active' => $act, 'notes' => 'secret', 'created_at' => $c, 'deleted_at' => $d]);
        }
    }

    private function schema(): Schema
    {
        return Schema::make()
            ->field('id', FieldType::Int)->field('uid', FieldType::Uuid)->field('name', FieldType::String)
            ->field('role', FieldType::String)->field('age', FieldType::Int)->field('active', FieldType::Bool)
            ->field('notes', FieldType::String)->field('created_at', FieldType::Time)->field('deleted_at', FieldType::Time)
            ->deny('notes')->defaultSort('-created_at')->tieBreaker('id')->pageSize(2, 3);
    }

    /** @param array<string, mixed> $q */
    private function names(array $q, ?Schema $s = null): array
    {
        $p = Lister::paginate(Person::query(), $s ?? $this->schema()->pageSize(50, 50), Params::fromArray($q));

        return array_map(fn ($m) => $m->name, $p->items);
    }

    public function test_default_sort_page_size_and_meta(): void
    {
        $p = Lister::paginate(Person::query(), $this->schema(), Params::fromArray([]));
        $this->assertSame(['Linus', '100% Real_Name'], array_map(fn ($m) => $m->name, $p->items));
        $this->assertSame(['page' => 1, 'page_size' => 2, 'has_next' => true, 'total' => 5, 'total_pages' => 3], $p->meta);
    }

    public function test_page_size_is_clamped_and_second_page_works(): void
    {
        $p = Lister::paginate(Person::query(), $this->schema(), Params::fromArray(['page_size' => '100', 'page' => '2']));
        $this->assertSame(3, $p->meta['page_size']);
        $this->assertSame(['page' => 2, 'page_size' => 3, 'has_next' => false, 'total' => 5, 'total_pages' => 2], $p->meta);
        $this->assertCount(2, $p->items);
    }

    public function test_first_page_holding_everything_skips_the_count_query(): void
    {
        DB::enableQueryLog();
        $p = Lister::paginate(Person::query()->where('role', 'admin'), $this->schema(), Params::fromArray([]));
        $this->assertSame(1, $p->meta['total']);
        $this->assertCount(1, DB::getQueryLog());
    }

    public function test_without_total(): void
    {
        $p = Lister::paginate(Person::query(), $this->schema(), Params::fromArray([]), withTotal: false);
        $this->assertArrayNotHasKey('total', $p->meta);
    }

    public function test_operators(): void
    {
        $this->assertSame(['Alan Turing', 'Grace Hopper'], $this->names(['filter' => ['role' => 'editor'], 'sort' => 'name']));
        $this->assertSame(['Ada Lovelace', 'Alan Turing'], $this->names(['filter' => ['name' => ['prefix' => 'a']], 'sort' => 'name']));
        $this->assertSame(['100% Real_Name'], $this->names(['filter' => ['name' => ['like' => '0% R']]]));
        $this->assertSame(['Alan Turing', 'Linus'], $this->names(['filter' => ['age' => ['between' => '40,60']], 'sort' => 'name']));
        $this->assertSame(['Ada Lovelace', 'Alan Turing', 'Grace Hopper'], $this->names(['filter' => ['role' => ['in' => 'admin,editor']], 'sort' => 'name']));
        $this->assertSame(['100% Real_Name', 'Linus'], $this->names(['filter' => ['role' => ['nin' => 'admin,editor']], 'sort' => 'name']));
        $this->assertSame(['Grace Hopper'], $this->names(['filter' => ['deleted_at' => ['null' => 'false']]]));
        $this->assertSame(['Grace Hopper'], $this->names(['filter' => ['active' => 'false']]));
        $this->assertSame(['Linus', '100% Real_Name'], $this->names(['filter' => ['created_at' => ['gte' => '2026-02-10']]]));
        $this->assertSame(['Ada Lovelace', 'Alan Turing'], $this->names(['filter' => ['id' => ['in' => '1,2']], 'sort' => 'id']));
        $this->assertSame(['Alan Turing', 'Grace Hopper'], $this->names(['filter' => ['role' => ['editor']], 'sort' => 'name'])); // repeated key = in
    }

    public function test_like_wildcards_in_user_input_are_literal(): void
    {
        $this->assertSame(['100% Real_Name'], $this->names(['filter' => ['name' => ['like' => '%']]])); // not "match everything"
        $this->assertSame(['100% Real_Name'], $this->names(['filter' => ['name' => ['like' => '_']]]));
    }

    public function test_sql_injection_attempts_are_values_or_errors_never_sql(): void
    {
        $this->assertSame([], $this->names(['filter' => ['name' => "x' OR '1'='1"]]));
        $this->assertSame(5, DB::table('people')->count());
        foreach ([['filter' => ['name; DROP TABLE people' => 'x']], ['sort' => 'name; DROP TABLE people'], ['filter' => ['age' => '1 OR 1=1']]] as $q) {
            try {
                $this->names($q);
                $this->fail('expected AppException');
            } catch (AppException $e) {
                $this->assertSame('invalid_argument', $e->errorCode);
            }
        }
        $this->assertSame(5, DB::table('people')->count());
    }

    public function test_denied_and_unknown_fields_look_the_same(): void
    {
        $msgs = [];
        foreach (['notes', 'nonexistent'] as $f) {
            try {
                $this->names(['filter' => [$f => 'x']]);
            } catch (AppException $e) {
                $msgs[] = $e->fields[0]['message'];
            }
        }
        $this->assertSame(['unknown filter field', 'unknown filter field'], $msgs);
        $this->expectException(AppException::class);
        $this->names(['sort' => 'notes']);
    }

    public function test_operator_must_fit_the_type(): void
    {
        $this->expectException(AppException::class);
        $this->names(['filter' => ['active' => ['gt' => '1']]]);
    }

    public function test_restrict_only_accumulates_and_typos_are_errors(): void
    {
        $s = $this->schema()->restrict(denyFilter: ['role']);
        try {
            $this->names(['filter' => ['role' => 'admin']], $s);
            $this->fail();
        } catch (AppException) {
        }
        $this->expectException(\LogicException::class);
        $this->schema()->restrict(denyFilter: ['rol']); // typo must not silently expose a field
    }

    public function test_allow_only_is_a_white_list(): void
    {
        $s = $this->schema()->allowOnlyFilter('role');
        $this->assertSame(['Ada Lovelace'], $this->names(['filter' => ['role' => 'admin']], $s));
        $this->expectException(AppException::class);
        $this->names(['filter' => ['age' => '36']], $s);
    }

    public function test_limits(): void
    {
        $this->expectException(AppException::class);
        Lister::paginate(Person::query(), $this->schema()->limits(20, 100, 3, 10), Params::fromArray(['page' => '100']));
    }

    public function test_too_many_sort_keys_and_bad_numbers(): void
    {
        try {
            Params::fromArray(['page' => 'abc']);
            $this->fail();
        } catch (AppException $e) {
            $this->assertSame('page', $e->fields[0]['field']);
        }
        $this->expectException(AppException::class);
        $this->names(['sort' => 'name,age,role,id']);
    }

    public function test_cursor_pagination_walks_all_rows_once(): void
    {
        $seen = [];
        $cursor = null;
        do {
            $p = Lister::cursor(Person::query(), $this->schema()->defaultSort('-id'), Params::fromArray(array_filter(['page_size' => '2', 'cursor' => $cursor])));
            foreach ($p->items as $m) {
                $seen[] = $m->id;
            }
            $cursor = $p->meta['next_cursor'];
        } while ($p->meta['has_next']);
        $this->assertSame([5, 4, 3, 2, 1], $seen);
    }

    public function test_page_serialises_like_the_go_services(): void
    {
        $p = Lister::paginate(Person::query(), $this->schema()->pageSize(1, 1), Params::fromArray([]), map: fn ($m) => ['name' => $m->name]);
        $this->assertSame('{"items":[{"name":"Linus"}],"meta":{"page":1,"page_size":1,"has_next":true,"total":5,"total_pages":5}}', json_encode($p));
    }
}
